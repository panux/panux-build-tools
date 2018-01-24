all: pkgen/pkgen.o

.godeps:
	go get github.com/urfave/cli gopkg.in/yaml.v2
	touch .godeps

%.o: %.go .godeps
	go build -o $@ $<

clean:
	rm $(shell find . -name *.o)

ifeq (,$(BINDIR))
BINDIR = usr/bin
endif

install: $(DESTDIR)/$(BINDIR)/buildcontainer $(DESTDIR)/$(BINDIR)/pkgen

$(DESTDIR)/$(BINDIR)/buildcontainer: buildcontainer.sh
	install -D -m 0755 $< $@
$(DESTDIR)/$(BINDIR)/pkgen: pkgen/pkgen.o
	install -D -m 0755 $< $@
