package route

import petname "github.com/dustinkirkland/golang-petname"

// RandomName returns a random two-word name like "frosty-fox" — DNS-safe,
// lowercase, dashed. Used by --random on serve/expose.
func RandomName() string {
	return petname.Generate(2, "-")
}
