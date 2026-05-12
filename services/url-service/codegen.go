package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

type ShortCodeGenerator struct{}

func NewShortCodeGenerator() *ShortCodeGenerator {
	return &ShortCodeGenerator{}
}

func (g *ShortCodeGenerator) Generate() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("crypto/rand read failed: %v", err))
	}
	n := new(big.Int).SetBytes(buf)
	maxCode := new(big.Int).Exp(big.NewInt(62), big.NewInt(7), nil)
	n.Mod(n, maxCode)
	return Encode(n)
}
