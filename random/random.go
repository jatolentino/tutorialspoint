// Package random provides facilities to generate random values.
// It leverages the default Source of standard 'math/rand' package
// that is thread-safe.
package random

import (
	crand "crypto/rand"
	"encoding/binary"
	"math/big"
	mrand "math/rand"
	"time"
)

const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// init initializes the default Source of 'math/rand' package.
// It tries to do so by using a random number generated by 'crypto/rand'
// package as seed.
// If the call to 'crypto/rand' fails, the startup timestamp in nanoseconds
// is used as seed.
//
// Why? because 'math/rand' is simpler to use, however we need to provide
// a random number as a seed to make this package more reliable.
// 'crypto/rand' is good for retrieving the initial seed but it
// can return an error, in such case we resort to a unix timestamp
// that should not be too bad for our use case.
func init() {
	var b [8]byte
	_, err := crand.Read(b[:])
	if err != nil {
		mrand.Seed(time.Now().UnixNano())
		return
	}
	mrand.Seed(int64(binary.LittleEndian.Uint64(b[:])))
}

// String returns a randomly generated string that can contain
// numbers, uppercase and lowercase letters.
func String(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[mrand.Intn(len(charset))]
	}
	return string(b)
}

// StringSecure returns a cryptographically secure randomly generated
// string that can contain numbers, uppercase and lowercase letters.
func StringSecure(length int) (string, error) {
	b := make([]byte, length)
	for i := range b {
		l := big.NewInt(int64(len(charset)))
		num, err := crand.Int(crand.Reader, l)
		if err != nil {
			return "", err
		}
		b[i] = charset[num.Int64()]
	}
	return string(b), nil
}
