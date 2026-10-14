# Installing Ostraka

Ostraka is a Go command-line program. Its normal installation is the Go
module workflow; no agent skill or Carthage-specific installation is required.

The supervisor includes the complete item context and the exact final-reply
command in each dispatched prompt. When an agent needs the static orientation
outside a supervisor dispatch, run `ostraka preamble`. These two surfaces are
the supported integration, so an Ostraka skill would be optional convenience
and is not part of the minimum install.

## Install a published release

The repository module path is `github.com/Speculative/ostraka`. After a release
has been tagged, install the CLI with:

```bash
go install github.com/Speculative/ostraka/cmd/ostraka@latest
```

To pin a release instead of following the newest one:

```bash
go install github.com/Speculative/ostraka/cmd/ostraka@v0.1.0
```

`go install` places the executable in `GOBIN`, or in `GOPATH/bin` when `GOBIN`
is empty. Put that directory on `PATH`. `go get` is not the install command
for executables anymore; it manages dependencies in a module's `go.mod`.

Initialise a project explicitly, then launch the TUI from that project:

```bash
cd /path/to/project
ostraka init
ostraka tui
```

Alternatively, `ostraka tui` from an uninitialised directory offers to create
`.ostraka/` there before opening the TUI. Declining the offer exits cleanly;
initialisation errors are reported without starting the TUI.

The TUI's agent provider is separate: the `claude` and/or `codex` command must
also be installed and authenticated for the provider the user selects.

## Check the installed version

```bash
ostraka version
```

The command prints `ostraka v0.4.0`, for example, and works outside an
initialized project. It reads Go's embedded build metadata, so versioned
`go install` releases report their Git tag without a separate version file
or special linker flags.

With the supported Go toolchain, checkout builds also embed a version derived
from Git: an exact release tag when built at that tag, or a Go pseudo-version
identifying an untagged commit. Local changes add `+dirty`. Builds without
version metadata (for example, `go build -buildvcs=false` from a checkout)
report `ostraka (devel)`. Git is not needed when running the installed binary.

Plain `go run ./cmd/ostraka version` also reports `ostraka (devel)` because
`go run` omits Git metadata by default. To check the checkout version directly:

```bash
go run -buildvcs=true ./cmd/ostraka version
```

## Install from a checkout

For local development or before the first public release:

```bash
git clone git@github.com:Speculative/ostraka.git
cd ostraka
go install ./cmd/ostraka
```

When working on Ostraka itself, use the checkout directly so a stale binary
cannot hide local changes:

```bash
go run ./cmd/ostraka <arguments>
```

To build into an explicit local bin directory instead:

```bash
mkdir -p "$HOME/.local/bin"
go build -o "$HOME/.local/bin/ostraka" ./cmd/ostraka
```

The module currently requires Go 1.26.5, as declared in `go.mod`.

## Publish a release

There is no separate package registry or installer to maintain for the normal
Go workflow. A release consists of a clean repository state, a semantic
version tag, and the tag pushed to the public repository:

```bash
go mod tidy
go test ./...
git tag v0.1.0
git push origin v0.1.0
GOPROXY=proxy.golang.org go list -m github.com/Speculative/ostraka@v0.1.0
```

Use a new tag for every published change; do not change the contents of an
existing tag. Once the proxy has indexed the tag, users can install it with
the versioned `go install` command above. Prebuilt archives and platform
package managers can be added later if downloads for non-Go users become a
requirement, but they are not needed for the standard Go installation path.

For the official module-publishing workflow, see the [Go publishing
guide](https://go.dev/doc/modules/publishing) and the [Go module reference for
`go install`](https://go.dev/ref/mod#go-install).

## Carthage

Carthage mounts the project tree into the agent container at `/workspace`. No
Ostraka skill needs to be installed into `.carthage/` or copied into a
container image. Build or install the CLI in the environment where the TUI
runs, and keep the project `.carthage/` files for container configuration.

If the CLI is built from the mounted checkout, use:

```bash
go run ./cmd/ostraka tui
```

If a released binary is installed in the container's `PATH`, use:

```bash
ostraka tui
```

The provider credentials and provider command are environment concerns, not
part of the Ostraka module or its installation.
