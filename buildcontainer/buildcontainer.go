package main

import (
	"archive/tar"
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	var out string
	flag.StringVar(&out, "o", "", "output file")
	flag.Parse()
	infiles := flag.Args()
	of, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0400)
	if err != nil {
		panic(err)
	}
	targs := map[string]string{}
	provs := map[string][]string{}
	ot := tar.NewWriter(of)
	defer ot.Close()
	//merge tars and keep track of targets
	for _, fname := range infiles {
		f, err := os.Open(fname)
		if err != nil {
			panic(err)
		}
		tr := tar.NewReader(f)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				panic(err)
			}
			err = ot.WriteHeader(h)
			if err != nil {
				panic(err)
			}
			preread := false
			if strings.HasPrefix(h.Name, "./etc/lpkg.d/alt.d/") {
				dir, file := filepath.Split(h.Name)
				dirpts := filepath.SplitList(dir)
				aname := dirpts[len(dirpts)-1]
				if file == ".target" { //it is a target - read and add to map
					var buf bytes.Buffer
					_, err = io.Copy(&buf, tr)
					if err != nil {
						panic(err)
					}
					bdat := buf.Bytes()
					targs[aname] = string(bdat)
					_, err = ot.Write(bdat)
					if err != nil {
						panic(err)
					}
					preread = true
				} else if strings.HasSuffix(file, ".provider") { //it is a provider
					provs[aname] = append(provs[aname], file)
				}
			}
			if !preread {
				_, err = io.Copy(ot, tr)
				if err != nil {
					panic(err)
				}
			}
		}
	}
	//now select targets and add symlinks
	for alt, targ := range targs {
		providers := provs[alt]
		sort.Strings(providers)
		hdr := tar.Header{
			Name:     targ,
			Typeflag: tar.TypeSymlink,
			Mode:     0777,
		}
		err = ot.WriteHeader(&hdr)
		if err != nil {
			panic(err)
		}
	}
	err = ot.Close()
	if err != nil {
		panic(err)
	}
}
