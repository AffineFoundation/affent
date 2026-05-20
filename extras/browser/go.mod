module github.com/affinefoundation/affent/extras/browser

go 1.24.0

require (
	github.com/affinefoundation/affent v0.0.0
	github.com/go-rod/rod v0.116.2
	github.com/go-rod/stealth v0.4.9
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.33.0 // indirect
	github.com/ysmood/fetchup v0.2.4 // indirect
	github.com/ysmood/goob v0.4.0 // indirect
	github.com/ysmood/got v0.40.0 // indirect
	github.com/ysmood/gson v0.7.3 // indirect
	github.com/ysmood/leakless v0.9.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
)

// During in-tree development the parent affent module is consumed via
// path replace so we don't have to publish + tag for every iteration.
// Drop this when extras/browser is tagged separately.
replace github.com/affinefoundation/affent => ../..
