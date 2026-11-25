# Go with the official msb Go SDK, not Bun/TypeScript

The tool started life as a planned Bun/TypeScript area of agtk, but once it became its own `gh` extension repo we chose Go: microsandbox ships an official Go SDK (embedded CGO FFI library, local and cloud backends), `gh` extension tooling is Go-first (`go-gh` for auth and repo context, `gh-extension-precompile`), and the TypeScript SDK is a napi-rs addon with no verified Bun support. The cost is a CGO toolchain and per-platform release builds, which a native TS addon would have required anyway.
