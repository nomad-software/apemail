// Package appicon holds the program's icon, built into the binary so that the
// program needs nothing beside it.
package appicon

import _ "embed"

// PNG is the program's icon, a 256×256 PNG.
//
//go:embed appicon.png
var PNG []byte
