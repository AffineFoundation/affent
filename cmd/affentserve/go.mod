module github.com/affinefoundation/affent/cmd/affentserve

go 1.24.0

require (
	github.com/affinefoundation/affent v0.0.0
	github.com/affinefoundation/affent/extras/browser v0.0.0
	github.com/affinefoundation/affent/extras/web v0.0.0
	github.com/google/uuid v1.6.0
	github.com/rs/zerolog v1.33.0
)

require (
	github.com/JohannesKaufmann/dom v0.2.0 // indirect
	github.com/JohannesKaufmann/html-to-markdown/v2 v2.5.0 // indirect
	github.com/andybalholm/cascadia v1.3.3 // indirect
	github.com/araddon/dateparse v0.0.0-20210429162001-6b43995a97de // indirect
	github.com/go-rod/rod v0.116.2 // indirect
	github.com/go-rod/stealth v0.4.9 // indirect
	github.com/go-shiori/dom v0.0.0-20230515143342-73569d674e1c // indirect
	github.com/go-shiori/go-readability v0.0.0-20251205110129-5db1dc9836f0 // indirect
	github.com/gogs/chardet v0.0.0-20211120154057-b7413eaefb8f // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ysmood/fetchup v0.2.4 // indirect
	github.com/ysmood/goob v0.4.0 // indirect
	github.com/ysmood/got v0.40.0 // indirect
	github.com/ysmood/gson v0.7.3 // indirect
	github.com/ysmood/leakless v0.9.0 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
)

// In-tree development: path-replace the parent module and sibling
// extras until they're tagged separately.
replace (
	github.com/affinefoundation/affent => ../..
	github.com/affinefoundation/affent/extras/browser => ../../extras/browser
	github.com/affinefoundation/affent/extras/web => ../../extras/web
)
