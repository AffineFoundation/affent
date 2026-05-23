package mcp

type RuntimeBoundaries struct {
	ToolResultBytes int
}

func DefaultRuntimeBoundaries() RuntimeBoundaries {
	return RuntimeBoundaries{
		ToolResultBytes: maxMCPToolResultBytes,
	}
}
