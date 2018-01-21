all: buildcontainer/buildcontainer.o pkgen/pkgen.o

godeps:
	go get -u github.com/urfave/cli gopkg.in/yaml.v2

%.o: %.go godeps
	go build -o $@ $<

clean:
	rm $(shell find . -name *.o)

ifeq (,$(BINDIR))
BINDIR = usr/bin
endif

install: $(DESTDIR)/$(BINDIR)/buildcontainer $(DESTDIR)/$(BINDIR)/pkgen

$(DESTDIR)/$(BINDIR)/buildcontainer: buildcontainer/buildcontainer.o
	install -D -m 0755 $< $@
$(DESTDIR)/$(BINDIR)/pkgen: pkgen/pkgen.o
	install -D -m 0755 $< $@
