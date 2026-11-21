package main

import (
	"crypto/sha256"
	"hash"
)

func newSHA() hash.Hash { return sha256.New() }
