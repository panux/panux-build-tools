package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

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

func loadPkgen(in io.Reader) (rpg *rawPackageGenerator, e error) {
	dat, err := ioutil.ReadAll(in)
	if err != nil {
		return nil, err
	}
	rpg = new(rawPackageGenerator)
	err = yaml.Unmarshal(dat, rpg)
	if err != nil {
		return nil, err
	}
	return
}

func tmpl(str []string, pg *rawPackageGenerator) ([]string, error) {
	t, err := template.New("pkgen").Parse(strings.Join(str, "\n"))
	if err != nil {
		return nil, err
	}
	buf := bytes.NewBuffer(nil)
	err = t.Execute(buf, pg)
	if err != nil {
		return nil, err
	}
	return strings.Split(buf.String(), "\n"), nil
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
TARS = $(foreach pkg,$(PKGS),tars/$(pkg).tar.gz)
$(TARS): build tars infos
	tar -cf $@ -C out/$(basename $@) .
outs: $(foreach pkg,$(PKGS),out/$(pkg)/.pkginfo)
out/%: out
	mkdir $@
out/%/.pkginfo: sources out/%
	cp src/.pkginfo/$(basename $(dirname $@)).pkginfo out/$(basename $(dirname $@))/.pkginfo
out tars src:
	mkdir $@
gentars: $(TARS)
sources:
	tar -xf $(SRCTAR) -C src
build: outs sources`

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
		inputpath := ctx.String("input")
		if inputpath == "-" {
			in = os.Stdin
		} else {
			f, err := os.Open(inputpath)
			if err != nil {
				return cli.NewExitError(err, 65)
			}
			in = f
		}
		outputpath := ctx.String("output")
		if outputpath == "-" {
			out = os.Stdout
		} else {
			f, err := os.OpenFile(outputpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
			if err != nil {
				return cli.NewExitError(err, 65)
			}
			out = f
		}
		pg, err := loadPkgen(in)
		if err != nil {
			return cli.NewExitError(err, 65)
		}
		if pg.Builder == "" {
			pg.Builder = "alpine"
		}
		pk = *pg
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
			},
			Action: func(ctx *cli.Context) error {
				_, err := fmt.Fprint(out, strings.Join(pk.BuildDependencies, ctx.String("seperator")))
				if err != nil {
					return cli.NewExitError(err, 65)
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
			Name:  "source, src",
			Usage: "generate commands to load sources",
			Action: func(ctx *cli.Context) error {
				tw := tar.NewWriter(out)
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
						err = tw.WriteHeader(&tar.Header{
							Name: fname,
							Mode: 0600,
							Size: len,
						})
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
					}
				}
				manifest := []byte(strings.Join(pk.Sources, "\n"))
				//write manifest
				err := tw.WriteHeader(&tar.Header{
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
				_, err := fmt.Fprintf(out, bspre, strings.Join(pklist, " "), pk.Version, pk.Build)
				if err != nil {
					return cli.NewExitError(err, 65)
				}
				for _, l := range pk.Script {
					_, err = fmt.Fprintf(out, "\n\t%s", l)
					if err != nil {
						return cli.NewExitError(err, 65)
					}
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
