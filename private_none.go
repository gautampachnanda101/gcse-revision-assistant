//go:build !private

package main

import "embed"

var privateEmbedded embed.FS

const hasPrivateEmbedded = false
