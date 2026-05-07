module github.com/affinefoundation/affent/extras/web

go 1.24.0

require (
	github.com/JohannesKaufmann/html-to-markdown/v2 v2.5.0
	github.com/affinefoundation/affent v0.0.0
	github.com/go-shiori/go-readability v0.0.0-20251205110129-5db1dc9836f0
)

require (
	github.com/JohannesKaufmann/dom v0.2.0 // indirect
	github.com/andybalholm/cascadia v1.3.3 // indirect
	github.com/araddon/dateparse v0.0.0-20210429162001-6b43995a97de // indirect
	github.com/go-shiori/dom v0.0.0-20230515143342-73569d674e1c // indirect
	github.com/gogs/chardet v0.0.0-20211120154057-b7413eaefb8f // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.33.0 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
)

// During in-tree development the parent affent module is consumed via
// path replace so we don't have to publish + tag for every iteration.
// Drop this when extras/web is tagged separately.
replace github.com/affinefoundation/affent => ../..
