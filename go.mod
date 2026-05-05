// affent: a small, embeddable agent loop core.
//
// Drop into any environment that talks to an OpenAI-compatible chat
// completions endpoint — affine-agents (gateway + UI), training rigs,
// eval pipelines, scripts. Deps are deliberately tiny: uuid for ids,
// zerolog for logging, stdlib for everything else.
module github.com/affinefoundation/affent

go 1.22

require (
	github.com/google/uuid v1.6.0
	github.com/rs/zerolog v1.33.0
)

require (
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	golang.org/x/sys v0.12.0 // indirect
)
