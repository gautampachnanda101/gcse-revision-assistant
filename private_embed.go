//go:build private

package main

import "embed"

// Personal builds (`go build -tags private .`) bake every pack in ./private/ into the binary, so they work
// offline with no extra files. Public release builds never use this tag, and private/ is gitignored.
//
//go:embed private/*.js
var privateEmbedded embed.FS

const hasPrivateEmbedded = true
