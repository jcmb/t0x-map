package combine

import _ "embed"

// Script is T0x_Combine.py — used server-side by /api/export to merge day files.
//
//go:embed T0x_Combine.py
var Script []byte
