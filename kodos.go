package kodos

import (
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Context contains all build specific values.
type Context struct {
	GOOS, GOARCH string
	Workdir      string
	Pkgdir       string
	Bindir       string
	force        bool     // always force build, even if not stale
	race         bool     // build a -race enabled binary
	gcflags      []string // -gcflags
	ldflags      []string // -ldflags
	buildtags    []string
}

func (c *Context) isCrossCompile() bool { return false }

func (c *Context) searchPaths() []string {
	return []string{
		c.Workdir,
		c.Pkgdir,
	}
}

// ctxString returns a string representation of the unique properties
// of the context.
func (c *Context) ctxString() string {
	v := []string{
		c.GOOS,
		c.GOARCH,
	}
	v = append(v, c.buildtags...)
	return strings.Join(v, "-")
}

// Package describes a set of Go files to be compiled.
type Package struct {
	*Context
	*build.Package
	Imports   []*Package
	testScope bool // is a test scoped package
	Main      bool // this is a command
	NotStale  bool // this package _and_ all its dependencies are not stale
}

const debug = true

func debugf(format string, args ...interface{}) {
	if !debug {
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// IsStale returns true if the source pkg is considered to be stale with
// respect to its installed version.
func (pkg *Package) IsStale() bool {
	switch pkg.ImportPath {
	case "C", "unsafe":
		// synthetic packages are never stale
		return false
	}

	if pkg.force {
		return true
	}

	// tests are always stale, they are never installed
	if pkg.testScope {
		return true
	}

	// Package is stale if completely unbuilt.
	var built time.Time
	if fi, err := os.Stat(pkg.pkgpath()); err == nil {
		built = fi.ModTime()
	}

	if built.IsZero() {
		debugf("%s is missing", pkg.pkgpath())
		return true
	}

	olderThan := func(file string) bool {
		fi, err := os.Stat(file)
		return err != nil || fi.ModTime().After(built)
	}

	newerThan := func(file string) bool {
		fi, err := os.Stat(file)
		return err != nil || fi.ModTime().Before(built)
	}

	// Package is stale if a dependency is newer.
	for _, p := range pkg.Imports {
		if p.Package.ImportPath == "C" || p.Package.ImportPath == "unsafe" {
			continue // ignore stale imports of synthetic packages
		}
		if olderThan(p.pkgpath()) {
			debugf("%s is older than %s", pkg.pkgpath(), p.pkgpath())
			return true
		}
	}

	// if the main package is up to date but _newer_ than the binary (which
	// could have been removed), then consider it stale.
	if pkg.Main && newerThan(pkg.Binfile()) {
		debugf("%s is newer than %s", pkg.pkgpath(), pkg.Binfile())
		return true
	}

	for _, src := range pkg.files() {
		if olderThan(filepath.Join(pkg.Dir, src)) {
			debugf("%s is older than %s", pkg.pkgpath(), filepath.Join(pkg.Dir, src))
			return true
		}
	}

	return false
}

// files returns all source files in scope
func (p *Package) files() []string {
	return stringList(p.GoFiles)
}

// pkgpath returns the destination for object cached for this Package.
func (pkg *Package) pkgpath() string {
	importpath := filepath.FromSlash(pkg.ImportPath) + ".a"
	switch {
	case pkg.isCrossCompile():
		return filepath.Join(pkg.Pkgdir, importpath)
	case pkg.race:
		// race enabled standard lib
		return filepath.Join(runtime.GOROOT(), "pkg", pkg.GOOS+"_"+pkg.GOARCH+"_race", importpath)
	default:
		return filepath.Join(pkg.Pkgdir, importpath)
	}
}

// Binfile returns the destination of the compiled target of this command.
func (pkg *Package) Binfile() string {
	// TODO(dfc) should have a check for package main, or should be merged in to objfile.
	target := filepath.Join(pkg.Bindir, pkg.binname())
	if pkg.testScope {
		target = filepath.Join(pkg.Workdir, filepath.FromSlash(pkg.ImportPath), "_test", pkg.binname())
	}

	// if this is a cross compile or GOOS/GOARCH are both defined or there are build tags, add ctxString.
	if pkg.isCrossCompile() || (os.Getenv("GOOS") != "" && os.Getenv("GOARCH") != "") {
		target += "-" + pkg.ctxString()
	} else if len(pkg.buildtags) > 0 {
		target += "-" + strings.Join(pkg.buildtags, "-")
	}

	if pkg.GOOS == "windows" {
		target += ".exe"
	}
	return target
}

func (pkg *Package) binname() string {
	switch {
	case pkg.testScope:
		return pkg.name() + ".test"
	case pkg.Main:
		return filepath.Base(filepath.FromSlash(pkg.ImportPath))
	default:
		panic("binname called with non main package: " + pkg.ImportPath)
	}
}

func (p *Package) complete() bool {
	return len(p.SFiles) == 0 // no cgo or runtime code
}

func (p *Package) name() string { return filepath.FromSlash(p.ImportPath) }

func stringList(args ...[]string) []string {
	var l []string
	for _, arg := range args {
		l = append(l, arg...)
	}
	return l
}

func (pkg *Package) Compile() error {
	if err := mkdir(filepath.Dir(pkg.pkgpath())); err != nil {
		return err
	}
	asm := func(pkg *Package, ofile, sfile string) error {
		args := []string{"-o", ofile, "-D", "GOOS_" + runtime.GOOS, "-D", "GOARCH_" + runtime.GOARCH}
		odir := filepath.Join(filepath.Dir(ofile))
		includedir := filepath.Join(runtime.GOROOT(), "pkg", "include")
		args = append(args, "-I", odir, "-I", includedir)
		args = append(args, sfile)
		if err := mkdir(filepath.Dir(ofile)); err != nil {
			return err
		}
		cmd := exec.Command(filepath.Join(runtime.GOROOT(), "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH, "asm"), args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Dir = pkg.Dir
		fmt.Fprintf(os.Stderr, "+ %s\n", strings.Join(cmd.Args, " "))
		return cmd.Run()
	}

	for _, sfile := range pkg.SFiles {
		if err := asm(pkg, filepath.Join(filepath.Dir(pkg.pkgpath()), strings.TrimSuffix(sfile, ".s")+".o"), sfile); err != nil {
			return err
		}
	}

	args := append(pkg.gcflags, "-p", pkg.ImportPath, "-pack")
	args = append(args, "-o", pkg.pkgpath())
	for _, d := range pkg.searchPaths() {
		args = append(args, "-I", d)
	}
	if pkg.ImportPath == "runtime" {
		// runtime compiles with a special gc flag to emit
		// additional reflect type data.
		args = append(args, "-+")
	}

	switch {
	case pkg.complete():
		args = append(args, "-complete")
	}

	args = append(args, pkg.GoFiles...)
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH, "compile"), args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = pkg.Dir
	fmt.Fprintf(os.Stderr, "+ %s\n", strings.Join(cmd.Args, " "))
	return cmd.Run()
}

func (pkg *Package) Link() error {
	// to ensure we don't write a partial binary, link the binary to a temporary file in
	// in the target directory, then rename.
	tmp, err := ioutil.TempFile(filepath.Dir(pkg.Binfile()), ".kang-link")
	if err != nil {
		return err
	}
	tmp.Close()

	args := append(pkg.ldflags, "-o", tmp.Name())
	for _, d := range pkg.searchPaths() {
		args = append(args, "-L", d)
	}
	args = append(args, "-buildmode", "exe")
	args = append(args, pkg.pkgpath())

	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH, "link"), args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = pkg.Workdir
	fmt.Fprintf(os.Stderr, "+ %s\n", strings.Join(cmd.Args, " "))
	if err := cmd.Run(); err != nil {
		os.Remove(tmp.Name()) // remove partial file
		return err
	}
	if err := mkdir(filepath.Dir(pkg.Binfile())); err != nil {
		os.Remove(tmp.Name()) // remove partial file
		return err
	}

	return rename(tmp.Name(), pkg.Binfile())
}

func mkdir(path string) error {
	return os.MkdirAll(path, 0755)
}

func rename(from, to string) error {
	fmt.Fprintf(os.Stderr, "+ mv %s %s\n", from, to)
	return os.Rename(from, to)
}
