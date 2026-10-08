package gf256_test

import (
	"fmt"

	"github.com/mstephenholl/graptor-q/gf256"
)

// A parity symbol of a Reed-Solomon code is a sum of source symbols, each
// scaled by a coefficient of the code's generator matrix.
func ExampleMulAddSlice() {
	sources := [][]byte{[]byte("NORM"), []byte("data")}
	coefs := []byte{0x8e, 0x03}
	parity := make([]byte, 4)
	for i, s := range sources {
		gf256.MulAddSlice(parity, s, coefs[i])
	}
	fmt.Printf("%x\n", parity)
	// Output: 8b0ab50b
}
