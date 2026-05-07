module github.com/affinefoundation/affent/extras/web

go 1.22

require (
	github.com/affinefoundation/affent v0.0.0
	golang.org/x/net v0.30.0
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/rs/zerolog v1.33.0 // indirect
	golang.org/x/sys v0.26.0 // indirect
)

// During in-tree development the parent affent module is consumed via
// path replace so we don't have to publish + tag for every iteration.
// Drop this when extras/web is tagged separately.
replace github.com/affinefoundation/affent => ../..
