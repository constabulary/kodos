package main

import (
	"crypto/sha1"
	"flag"
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/constabulary/kodos"
)

func check(err error) {
	if err != nil {
		fatal(err)
		os.Exit(1)
	}
}

func fatal(arg interface{}, args ...interface{}) {
	fmt.Fprint(os.Stderr, "fatal: ", arg)
	fmt.Fprintln(os.Stderr, args...)
	os.Exit(1)
}

func main() {
	flag.Parse()
	dir, err := findreporoot(cwd())
	check(err)

	fmt.Println("Using", dir)

	workdir, err := ioutil.TempDir("", "kodos")
	check(err)

	pkgdir := filepath.Join(dir, ".kodos", "pkg")

	ctx := &kodos.Context{
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
		Workdir: workdir,
		Pkgdir:  pkgdir,
		Bindir:  dir,
	}

	action := "build"
	prefix := "github.com/constabulary/kodos"

	switch action {
	case "build":
		srcs := loadSources(prefix, dir)
		for _, src := range srcs {
			fmt.Printf("loaded %s (%s)\n", src.ImportPath, src.Name)
		}

		srcs = loadDependencies(dir, srcs...)

		pkgs := transform(ctx, srcs...)
		computeStale(pkgs...)

		targets := make(map[string]func() error)
		fn, err := buildPackages(targets, pkgs...)
		check(err)
		check(fn())
	default:
		fatal("unknown action:", action)
	}
}

func cwd() string {
	wd, err := os.Getwd()
	check(err)
	return wd
}

// transform takes a slice of go/build.Package and returns the
// corresponding slice of kodos.Packages.
func transform(ctx *kodos.Context, v ...*build.Package) []*kodos.Package {
	srcs := make(map[string]*build.Package)
	for _, pkg := range v {
		srcs[pkg.ImportPath] = pkg
	}

	var pkgs []*kodos.Package
	seen := make(map[string]bool)

	var walk func(src *build.Package)
	walk = func(src *build.Package) {
		if seen[src.ImportPath] {
			return
		}
		seen[src.ImportPath] = true

		for _, i := range src.Imports {
			pkg, ok := srcs[i]
			if !ok {
				fatal("transform: pkg ", i, "is not loaded")
			}
			walk(pkg)
		}

		pkgs = append(pkgs, &kodos.Package{
			Context:    ctx,
			ImportPath: src.ImportPath,
			Dir:        src.Dir,
			GoFiles:    src.GoFiles,
			Main:       src.Name == "main",
		})
	}
	for _, p := range v {
		walk(p)
	}
	return pkgs
}

// computeStale sets the UpToDate flag on a set of package roots.
func computeStale(roots ...*kodos.Package) {
	seen := make(map[*kodos.Package]bool)

	var walk func(pkg *kodos.Package) bool
	walk = func(pkg *kodos.Package) bool {
		if seen[pkg] {
			return pkg.NotStale
		}
		seen[pkg] = true

		for _, i := range pkg.Imports {
			if !walk(i) {
				// a dep is stale so we are stale
				return false
			}
		}

		stale := pkg.IsStale()
		pkg.NotStale = !stale
		return !stale
	}

	for _, root := range roots {
		walk(root)
	}
}

// findreporoot returns the location of the closest .git directory
// relative to the dir provided.
func findreporoot(dir string) (string, error) {
	orig := dir
	for {
		path := filepath.Join(dir, ".git")
		fi, err := os.Stat(path)
		if err == nil && fi.IsDir() {
			return dir, nil
		}
		if err != nil && !os.IsNotExist(err) {
			check(err)
		}
		d := filepath.Dir(dir)
		if d == dir {
			// got to the root directory without
			return "", fmt.Errorf("could not locate .git in %s", orig)
		}
		dir = d
	}
}

func buildPackages(targets map[string]func() error, pkgs ...*kodos.Package) (func() error, error) {
	var deps []func() error
	for _, pkg := range pkgs {
		fn, err := buildPackage(targets, pkg)
		check(err)
		deps = append(deps, fn)
	}
	return func() error {
		for _, fn := range deps {
			if err := fn(); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

func buildPackage(targets map[string]func() error, pkg *kodos.Package) (func() error, error) {

	// if this action is already present in the map, return it
	// rather than creating a new action.
	if fn, ok := targets[pkg.ImportPath]; ok {
		return fn, nil
	}

	// step 0. are we stale ?
	// if this package is not stale, then by definition none of its
	// dependencies are stale, so ignore this whole tree.
	if pkg.NotStale {
		return func() error {
			fmt.Println(pkg.ImportPath, "is up to date")
			return nil
		}, nil
	}

	// step 1. build dependencies
	var deps []func() error
	for _, pkg := range pkg.Imports {
		fn, err := buildPackage(targets, pkg)
		if err != nil {
			return nil, err
		}
		deps = append(deps, fn)
	}

	// step 2. build this package
	build := func() error {
		for _, dep := range deps {
			if err := dep(); err != nil {
				return err
			}
		}
		if err := pkg.Compile(); err != nil {
			return err
		}
		if !pkg.Main {
			return nil // we're done
		}
		return pkg.Link()
	}

	// record the final action as the action that represents
	// building this package.
	targets[pkg.ImportPath] = build

	return build, nil
}

func loadSources(prefix string, dir string) []*build.Package {
	f, err := os.Open(dir)
	check(err)
	files, err := f.Readdir(-1)
	check(err)
	f.Close()

	var srcs []*build.Package
	for _, fi := range files {
		name := fi.Name()
		if strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" {
			// ignore it
			continue
		}
		if fi.IsDir() {
			srcs = append(srcs, loadSources(path.Join(prefix, name), filepath.Join(dir, name))...)
		}
	}

	pkg, err := build.ImportDir(dir, 0)
	switch err := err.(type) {
	case nil:
		// ImportDir does not know the import path for this package
		// but we know the prefix, so fix it.
		pkg.ImportPath = prefix
		srcs = append(srcs, pkg)
	case (*build.NoGoError):
		// do nothing
	default:
		check(err)
	}

	return srcs
}

func loadDependencies(rootdir string, srcs ...*build.Package) []*build.Package {
	load := func(path string) *build.Package {
		fmt.Println("searching", path, "in", filepath.Join(runtime.GOROOT(), "src"), "(GOROOT)")
		dir := filepath.Join(runtime.GOROOT(), "src", path)
		if _, err := os.Stat(dir); err != nil {
			fatal("cannot resolve path ", path, err.Error())
		}
		return importPath(path, dir)
	}

	seen := make(map[string]bool)
	var walk func(string)
	walk = func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		pkg := load(path)
		srcs = append(srcs, pkg)
		for _, i := range pkg.Imports {
			walk(i)
		}
	}
	for _, src := range srcs {
		seen[src.ImportPath] = true
	}
	for _, src := range srcs[:] {
		for _, i := range src.Imports {
			walk(i)
		}
	}
	return srcs
}

func register(rootdir, prefix, kind, arg string, next func(string) *build.Package) func(string) *build.Package {
	dir := cacheDir(rootdir, prefix+kind+"="+arg)
	fmt.Println("registered:", prefix, "@", arg)
	return func(path string) *build.Package {
		if !strings.HasPrefix(path, prefix) {
			return next(path)
		}
		fmt.Println("searching", path, "in", prefix, "@", arg)
		dir := filepath.Join(dir, path)
		_, err := os.Stat(dir)
		if os.IsNotExist(err) {
			check(err)
		}
		return importPath(path, dir)
	}
}

func importPath(path, dir string) *build.Package {
	pkg, err := build.ImportDir(dir, 0)
	check(err)
	// ImportDir does not know the import path for this package
	// but we know the prefix, so fix it.
	pkg.ImportPath = path
	return pkg
}

func cacheDir(rootdir, key string) string {
	hash := sha1.Sum([]byte(key))
	return filepath.Join(rootdir, ".kang", "cache", fmt.Sprintf("%x", hash[0:1]), fmt.Sprintf("%x", hash[1:]))
}
