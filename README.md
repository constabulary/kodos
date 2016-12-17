# kodos
![Kang of Regil VII](https://tstoaddicts.files.wordpress.com/2013/11/kodos.gif)

## What is this?

Many folks are frustated with the `$GOPATH` single workspace model, it doesn't let them check out the source of a project in a directory of their choice and it does not let them have multiple copies of a project checked out at the same time.
Similarly many folks are frustrated with the `gb` project model, which gives you the ability to check out a project anywhere you want, but has no solution for libraries, and gb projects are not go gettable.

kang is an experiment to try to provide a solution that:

1. lets you checkout a project anywhere you want
2. does not force you to give up interoperability with go get.

## Kodos uses .git

The reason `$GOPATH` is required by the go tool is twofold

1. all `import` statements are resolved by the go tool relative to a fixed absolute root; `$GOPATH/src`.
2. a location to store dependencies fetched by `go get`.

gb showed that point 2 could be solved by writing a new build tool that did not wrap the go tool and thus was not forced to reorganise the world to fit into the `$GOPATH` model.

The problem with point 1 is whenever the go tool builds a package in a directory, it must answer the question of "what is the full import path of this package".
The `$GOPATH` model answers this question by subtracting the prefix of `$GOPATH/src` from the path to the directory of the current package; the remainder is the package's fully qualified import path.
This is why if you checkout a package outside a `$GOPATH` workspace, the go tool cannot figure out the packages' fully qualified import path and everything falls apart.

## Installation

kodos requires Go 1.7.3 or later.

    go get github.com/constabulary/kodos/...

## Usage

_note_: not done, see roadmap and TODO.

`kodos build` will build all the source in a project, it can be issued anywhere in the project.
`kodos test` will test all the packages in a project, ditto.

Both commands automatically cache as much as possible for fast incremental compilation.

## Roadmap

Here are the big ticket items before kang is a working proof of concept.

- [ ] kodos test support.
- [ ] automatic dependency fetching.
- [ ] cgo support.
- [ ] cross compile support.

## TODO

Lots to do.

- [ ] unit tests
- [ ] functional and integration tests
- [ ] kang has forked a number of gb components, merge the changes required back into gb so both kang and gb use the same build and test primatives.

## Contributing

Bug reports are most welcome.

Pull requests must include a `fixes #NNN` or `updates #NNN` comment. 

Please discuss your design on the accompanying issue before submitting a pull request. If there is no suitable issue, please open one to discuss the feature before slinging code. Thank you.
