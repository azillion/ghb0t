# golint-fixer

[![Travis CI](https://img.shields.io/travis/azillion/ghb0t.svg?style=for-the-badge)](https://travis-ci.org/genuinetools/ghb0t)

[@golint-fixer](https://github.com/golint-fixer)

A GitHub Bot to automatically create pull requests to fix golint imports.

> **NOTE:** You probably don't want to run this.

> **NOTE:** Also, the stuff I wrote is messy, one-off, go code. The good code is from [Genuine Tools](https://github.com/genuinetools)

> **NOTE:** Also, also, if you hate me for opening so many PRs please let me know on [Twitter](https://twitter.com/alex_zillion).

 * [Installation](README.md#installation)
      * [Via Go](README.md#via-go)
 * [Usage](README.md#usage)

## Installation

#### Via Go

```console
$ go get github.com/azillion/golint-fixer
```

## Usage

```console
$ golint-fixer -h
golint-fixer -  A GitHub Bot to automatically create pull requests to fix golint imports.

Usage: golint-fixer <command>

Flags:

  -d         enable debug logging (default: false)
  -interval  check interval (ex. 5ms, 10s, 1m, 3h) (default: 30s)
  -token     GitHub API token (or env var GITHUB_TOKEN) 
  -url       Connect to a specific GitHub server, provide full API URL (ex. https://github.example.com/api/v3/) (default: <none>)

Commands:

  version  Show the version information.
```
