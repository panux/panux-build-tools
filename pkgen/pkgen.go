package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/template"

	"github.com/panux/encoding-sh"
	"github.com/urfave/cli"
	yaml "gopkg.in/yaml.v2"
)

type rawPackageGenerator struct {
	SrcPath           string
	Packages          pkgmap
	OneShell          bool
	Version           string
	Build             uint
	Sources           []string
	Script            []string
	BuildDependencies []string
	Builder           string
	Cross             bool
	Data              map[string]interface{}
}
type pkgmap map[string]pkg
type pkg struct {
	Dependencies []string
}

func loadPkgen(in io.Reader, hostarch string, buildarch string) (pg *rawPackageGenerator, e error) {
	dat, err := ioutil.ReadAll(in)
	if err != nil {
		return nil, err
	}
	pg = new(rawPackageGenerator)
	err = yaml.Unmarshal(dat, pg)
	if err != nil {
		return nil, err
	}
	if pg.Builder == "" {
		pg.Builder = "alpine"
	}
	pg.Script, err = tmpl(pg.Script, pg, hostarch, buildarch)
	if err != nil {
		return nil, err
	}
	pg.Sources, err = tmpl(pg.Sources, pg, hostarch, buildarch)
	if err != nil {
		return nil, err
	}
	return
}

func tmpl(str []string, pg *rawPackageGenerator, hostarch string, buildarch string) ([]string, error) {
	t, err := template.New("pkgen").Funcs(map[string]interface{}{
		"make": func(dir string, args ...string) string {
			lines := make([]string, len(args))
			for i, a := range args {
				lines[i] = fmt.Sprintf("$(MAKE) -C %s %s", dir, a)
			}
			return strings.Join(lines, "\n")
		},
		"extract": func(name string, ext string) string {
			return strings.Join(
				[]string{
					fmt.Sprintf("tar -xf src/%s-%s.tar.%s", name, pg.Version, ext),
					fmt.Sprintf("mv %s-%s %s", name, pg.Version, name),
				},
				"\n")
		},
		"pkmv": func(file string, srcpkg string, destpkg string) string {
			if strings.HasSuffix(file, "/") { //cut off trailing /
				file = file[:len(file)-2]
			}
			dir, _ := filepath.Split(file)
			mv := fmt.Sprintf("mv %s %s",
				filepath.Join("out", srcpkg, file),
				filepath.Join("out", destpkg, dir),
			)
			if dir != "" {
				return strings.Join([]string{
					fmt.Sprintf("mkdir -p %s", filepath.Join("out", destpkg, dir)),
					mv,
				}, "\n")
			}
			return mv
		},
		"mvman": func(pkg string) string {
			return fmt.Sprintf("mkdir -p out/%s-man/usr/share\nmv out/%s/usr/share/man out/%s-man/usr/share/man", pkg, pkg, pkg)
		},
		"configure": func(dir string) string {
			if pg.Data["configure"] == nil {
				pg.Data["configure"] = []interface{}{}
			}
			car := pg.Data["configure"].([]interface{})
			ca := make([]string, len(car))
			for i, v := range car {
				ca[i] = v.(string)
			}
			return fmt.Sprintf("(cd %s && ./configure %s)", dir, strings.Join(ca, " "))
		},
		"confarch": func() string {
			if buildarch == "x86" {
				return "i386"
			}
			return buildarch
		},
		"hostarch": func() string {
			return hostarch
		},
		"buildarch": func() string {
			return buildarch
		},
	}).Parse(strings.Join(str, "\n"))
	if err != nil {
		return nil, err
	}
	buf := bytes.NewBuffer(nil)
	err = t.Execute(buf, pg)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSpace(buf.String()), "\n"), nil
}

type cmd struct {
	Argv []string
	Out  string
}

func quote(str string) string {
	return fmt.Sprintf("%q", str)
}

func quoteArr(strs []string) []string {
	o := make([]string, len(strs))
	for i, v := range strs {
		o[i] = quote(v)
	}
	return o
}

func fmtLua(c *cmd) string {
	return fmt.Sprintf("{argv = {%s}, out = %q}", strings.Join(quoteArr(c.Argv), ","), c.Out)
}

func toLua(c []*cmd) string {
	o := make([]string, len(c))
	for i, v := range c {
		o[i] = fmtLua(v)
	}
	return fmt.Sprintf("{%s}", strings.Join(o, ","))
}

func genSrcCmd(str string) (*cmd, error) {
	u, err := url.Parse(str)
	if err != nil {
		return nil, err
	}
	fname := filepath.Base(u.Path)
	var argv []string
	switch u.Scheme {
	case "https":
		argv = []string{"wget", u.String(), "-o", fname}
	case "file":
		argv = []string{"cp", "-f", "../" + u.Path, fname}
	default:
		return nil, fmt.Errorf("Unrecognized scheme: %s", u.Scheme)
	}
	return &cmd{Argv: argv, Out: fname}, nil
}

func prule(w io.Writer, name string, deps []string, cmds []string) error {
	_, err := fmt.Fprintf(w, "%s: %s\n\t%s",
		name,
		strings.Join(deps, " "),
		strings.Join(cmds, "\n\t"),
	)
	if err != nil {
		return err
	}
	return nil
}

var bspre = `all: gentars
.PHONY: gentars tars out src outs build
OUTDIR = $(shell pwd)/out
PKGS = %s
VERSION = %s
BUILDNUM = %d
OUTDIRS = %s
OUTS = %s
TARS = $(foreach pkg,$(PKGS),tars/$(pkg).tar.gz)
$(TARS): build tars $(OUTS)
	tar -cf $@ -C out/$(shell basename $@ .tar.gz) .
$(OUTDIRS): out
	mkdir $@
$(OUTS): sources $(OUTDIRS)
	cp src/.pkginfo/$(shell basename $(@D)).pkginfo $@
out tars src:
	mkdir $@
gentars: $(TARS)
sources: src
	tar -xf $(SRCTAR) -C src
.ONESHELL:
build: $(OUTS) sources`

func main() {
	app := cli.NewApp()
	var thisarch string
	switch runtime.GOARCH {
	case "amd64":
		thisarch = "x86_64"
	case "i386":
		thisarch = "x86"
	default:
		panic("Unrecognized arch")
	}
	//set app info
	app.Version = "2.0"
	//global flags
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "input, i",
			Value: "-",
			Usage: "input file",
		},
		cli.StringFlag{
			Name:  "output, o",
			Value: "-",
			Usage: "output file",
		},
		cli.StringFlag{
			Name:   "host",
			EnvVar: "HOSTARCH",
			Value:  thisarch,
			Usage:  "arch of host",
		},
		cli.StringFlag{
			Name:   "build",
			EnvVar: "BUILDARCH",
			Value:  thisarch,
			Usage:  "arch of target system",
		},
	}
	//management of inputs and outputs
	var in io.ReadCloser
	var out io.WriteCloser
	var pk rawPackageGenerator
	bef := func(ctx *cli.Context) error {
		inputpath := ctx.GlobalString("input")
		if inputpath == "-" {
			in = os.Stdin
		} else {
			f, err := os.Open(inputpath)
			if err != nil {
				return cli.NewExitError(err, 65)
			}
			in = f
		}
		outputpath := ctx.GlobalString("output")
		if outputpath == "-" {
			out = os.Stdout
		} else {
			f, err := os.OpenFile(outputpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
			if err != nil {
				return cli.NewExitError(err, 65)
			}
			out = f
		}
		pg, err := loadPkgen(in, ctx.GlobalString("host"), ctx.GlobalString("build"))
		if err != nil {
			return cli.NewExitError(err, 65)
		}
		pk = *pg
		if len(pk.Sources) == 1 {
			if pk.Sources[0] == "" {
				pk.Sources = []string{}
			}
		}
		return nil
	}
	app.After = func(ctx *cli.Context) error {
		if in != nil {
			err := in.Close()
			if err != nil {
				return cli.NewExitError(err, 65)
			}
		}
		if out != nil {
			err := out.Close()
			if err != nil {
				return cli.NewExitError(err, 65)
			}
		}
		os.Stdout.Close()
		return nil
	}
	//commands
	app.Commands = []cli.Command{
		{
			Name:    "builddeps",
			Aliases: []string{"bd", "bdeps"},
			Usage:   "list build dependencies",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "seperator, sep, s",
					Value: "\n",
					Usage: "seperator to use for output",
				},
				cli.BoolFlag{
					Name:  "n",
					Usage: "disable trailing seperator",
				},
			},
			Action: func(ctx *cli.Context) error {
				_, err := fmt.Fprint(out, strings.Join(pk.BuildDependencies, ctx.String("seperator")))
				if err != nil {
					return cli.NewExitError(err, 65)
				}
				if !ctx.Bool("n") {
					_, err = fmt.Fprint(out, ctx.String("seperator"))
					if err != nil {
						return cli.NewExitError(err, 65)
					}
				}
				return nil
			},
			Before: bef,
		},
		{
			Name:  "pkgs",
			Usage: "list build dependencies",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "seperator, sep, s",
					Value: "\n",
					Usage: "seperator to use for output",
				},
				cli.BoolFlag{
					Name:  "n",
					Usage: "disable trailing seperator",
				},
			},
			Action: func(ctx *cli.Context) error {
				pkgs := make([]string, len(pk.Packages))
				i := 0
				for n := range pk.Packages {
					pkgs[i] = n
					i++
				}
				sort.Strings(pkgs)
				_, err := fmt.Fprint(out, strings.Join(pkgs, ctx.String("seperator")))
				if err != nil {
					return cli.NewExitError(err, 65)
				}
				if !ctx.Bool("n") {
					_, err = fmt.Fprint(out, ctx.String("seperator"))
					if err != nil {
						return cli.NewExitError(err, 65)
					}
				}
				return nil
			},
			Before: bef,
		},
		{
			Name:    "deps",
			Aliases: []string{"d", "dep"},
			Usage:   "list build dependencies of a package",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "package, pkg, p",
					Value: "",
					Usage: "package to list dependencies of",
				},
				cli.StringFlag{
					Name:  "seperator, sep, s",
					Value: "\n",
					Usage: "seperator to use for output",
				},
				cli.BoolFlag{
					Name:  "n",
					Usage: "disable trailing seperator",
				},
			},
			Action: func(ctx *cli.Context) error {
				pkg := ctx.String("package")
				if pkg == "" {
					return cli.NewExitError("Missing flag: package", 65)
				}
				_, err := fmt.Fprint(out, strings.Join(pk.Packages[pkg].Dependencies, ctx.String("seperator")))
				if err != nil {
					return cli.NewExitError(err, 65)
				}
				if !ctx.Bool("n") {
					_, err = fmt.Fprint(out, ctx.String("seperator"))
					if err != nil {
						return cli.NewExitError(err, 65)
					}
				}
				return nil
			},
			Before: bef,
		},
		{
			Name:  "builder",
			Usage: "get builder for package",
			Action: func(ctx *cli.Context) error {
				_, err := fmt.Fprint(out, pk.Builder)
				if err != nil {
					return cli.NewExitError(err, 65)
				}
				return nil
			},
			Before: bef,
		},
		{
			Name:    "source",
			Aliases: []string{"src"},
			Usage:   "generate commands to load sources",
			Action: func(ctx *cli.Context) error {
				if ctx.GlobalString("input") != "-" {
					err := os.Chdir(filepath.Dir(ctx.GlobalString("input")))
					if err != nil {
						return cli.NewExitError(err, 65)
					}
				}
				tw := tar.NewWriter(out)
				defer tw.Close()
				pk.Sources = append(pk.Sources, "file://./pkgen.yaml")
				for _, s := range pk.Sources {
					u, err := url.Parse(s)
					if err != nil {
						return cli.NewExitError(err, 65)
					}
					_, fname := filepath.Split(u.Path)
					switch u.Scheme {
					case "https":
						g, err := http.Get(u.String())
						if err != nil {
							return cli.NewExitError(err, 65)
						}
						var stream io.Reader
						var len int64
						if g.ContentLength == -1 {
							buf := bytes.NewBuffer(nil)
							_, err = io.Copy(buf, g.Body)
							if err != nil {
								return cli.NewExitError(buf, 65)
							}
							stream = buf
							len = int64(buf.Len())
							err = g.Body.Close()
							if err != nil {
								return cli.NewExitError(err, 65)
							}
							g = nil
						} else {
							stream = g.Body
							len = g.ContentLength
						}
						h := &tar.Header{
							Name: fname,
							Mode: 0600,
							Size: len,
						}
						err = tw.WriteHeader(h)
						if err != nil {
							return cli.NewExitError(err, 65)
						}
						_, err = io.Copy(tw, stream)
						if err != nil {
							return cli.NewExitError(err, 65)
						}
						if g != nil {
							err = g.Body.Close()
							if err != nil {
								return cli.NewExitError(err, 65)
							}
						}
					case "file":
						var fload func(string) error
						fload = func(p string) error {
							p = strings.TrimLeft(p, "/")
							f, err := os.Open(p)
							if err != nil {
								return cli.NewExitError(err, 65)
							}
							defer f.Close()
							info, err := f.Stat()
							if err != nil {
								return cli.NewExitError(err, 65)
							}
							if info.Mode().IsDir() {
								err = tw.WriteHeader(&tar.Header{
									Name:     p,
									Mode:     int64(info.Mode()),
									Typeflag: tar.TypeDir,
								})
								infs, err := f.Readdir(0)
								if err != nil {
									return cli.NewExitError(err, 65)
								}
								for _, v := range infs {
									err = fload(filepath.Join(p, v.Name()))
									if err != nil {
										return cli.NewExitError(err, 65)
									}
								}
							} else {
								h := &tar.Header{
									Name: p,
									Mode: int64(info.Mode()),
									Size: info.Size(),
								}
								err = tw.WriteHeader(h)
								if err != nil {
									return cli.NewExitError(err, 65)
								}
								_, err = io.Copy(tw, f)
								if err != nil {
									return cli.NewExitError(err, 65)
								}
							}
							return nil
						}
						err := fload(u.Path)
						if err != nil {
							return cli.NewExitError(err, 65)
						}
					default:
						return cli.NewExitError(fmt.Errorf("Unrecognized scheme %q", u.Scheme), 65)
					}
				}
				//add pkginfos
				err := tw.WriteHeader(&tar.Header{
					Name:     ".pkginfo",
					Typeflag: tar.TypeDir,
					Mode:     0600,
				})
				if err != nil {
					return cli.NewExitError(err, 65)
				}
				for name, dat := range pk.Packages {
					if err != nil {
						return cli.NewExitError(err, 65)
					}
					pkginfo, err := sh.Encode(struct {
						Name         string
						Version      string
						Dependencies []string
					}{
						Name:         name,
						Version:      pk.Version,
						Dependencies: dat.Dependencies,
					})
					if err != nil {
						return cli.NewExitError(err, 65)
					}
					err = tw.WriteHeader(&tar.Header{
						Name: ".pkginfo/" + name + ".pkginfo",
						Mode: 0600,
						Size: int64(len(pkginfo)),
					})
					if err != nil {
						return cli.NewExitError(err, 65)
					}
					_, err = tw.Write(pkginfo)
					if err != nil {
						return cli.NewExitError(err, 65)
					}
				}
				manifest := []byte(strings.Join(pk.Sources, "\n"))
				//write manifest
				err = tw.WriteHeader(&tar.Header{
					Name: "manifest.txt",
					Mode: 0600,
					Size: int64(len(manifest)),
				})
				if err != nil {
					return cli.NewExitError(err, 65)
				}
				_, err = tw.Write(manifest)
				if err != nil {
					return cli.NewExitError(err, 65)
				}
				err = tw.Close()
				if err != nil {
					return cli.NewExitError(err, 65)
				}
				return nil
			},
			Before: bef,
		},
		{
			Name:  "build",
			Usage: "generate build script",
			Action: func(ctx *cli.Context) error {
				pklist := make([]string, len(pk.Packages))
				i := 0
				for name := range pk.Packages {
					pklist[i] = name
					i++
				}
				sort.Strings(pklist)
				outdirs := make([]string, len(pklist))
				outs := make([]string, len(pklist))
				for i, v := range pklist {
					outdirs[i] = fmt.Sprintf("out/%s", v)
					outs[i] = fmt.Sprintf("out/%s/.pkginfo", v)
				}
				_, err := fmt.Fprintf(out, bspre,
					strings.Join(pklist, " "),
					pk.Version,
					pk.Build,
					strings.Join(outdirs, " "),
					strings.Join(outs, " "),
				)
				if err != nil {
					return cli.NewExitError(err, 65)
				}
				for _, l := range pk.Script {
					_, err = fmt.Fprintf(out, "\n\t%s", l)
					if err != nil {
						return cli.NewExitError(err, 65)
					}
				}
				_, err = fmt.Println()
				if err != nil {
					return cli.NewExitError(err, 65)
				}
				return nil
			},
			Before: bef,
		},
	}
	//run
	sort.Sort(cli.FlagsByName(app.Flags))
	sort.Sort(cli.CommandsByName(app.Commands))
	app.Run(os.Args)
}
