#!/bin/sh

# usage: buildcontainer output.tar[.gz] pkg1.tar.gz . . .

set -e

# dir to create temp dir in (/tmp by default, but may not always work)
if [ -z "$TDIR" ]; then
    TDIR=/tmp
fi
# path to lpkg
if [ -z "$LPKGPATH" ]; then
    if [ -e /usr/bin/lpkg ]; then
        # bootstrapping panux from panux - nothing special
        echo "Nothing special"
    elif [ -e /usr/local/lpkg ]; then
        export PATH="$PATH:/usr/local/lpkg"
    else
        echo Failed to locate lpkg >> /dev/stderr
        exit 1
    fi
else
    export PATH="$PATH:$LPKGPATH"
fi

# prep arguments
if [ $# -lt 2 ]; then
    echo Insufficient arguments >> /dev/stderr
    exit 1
fi
TAROUT=$1
shift

# create temp dir to bootstrap in
TEMPDIR=$(mktemp -d $TDIR/tmp.XXXXXXXXXX)
deldir() {
    rm -rf $TEMPDIR
}
trap deldir EXIT

# create database dir
mkdir -p $TEMPDIR/etc/lpkg.d/db

# set ROOTFS variable for lpkg
export ROOTFS=$TEMPDIR
# install tars
while [ $# != 0 ]; do
    lpkg-inst $1
    shift
done

# tar output
tar -cf $TAROUT -C $TEMPDIR .
