package main

import "math/big"

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

const shortCodeLength = 7

func Encode(n *big.Int) string {
	result := make([]byte, shortCodeLength)
	sixty2 := big.NewInt(62)
	mod := new(big.Int)
	for i := shortCodeLength - 1; i >= 0; i-- {
		n.DivMod(n, sixty2, mod)
		result[i] = base62Alphabet[mod.Int64()]
	}
	return string(result)
}
