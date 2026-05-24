package mcp

type RuntimeBoundaries struct {
	ToolResultBytes       int
	HTTPJSONResponseBytes int
	HTTPSSELineBytes      int
	StdioFrameBytes       int
}

func DefaultRuntimeBoundaries() RuntimeBoundaries {
	return RuntimeBoundaries{
		ToolResultBytes:       maxMCPToolResultBytes,
		HTTPJSONResponseBytes: maxHTTPJSONResponseBytes,
		HTTPSSELineBytes:      maxHTTPSSELineBytes,
		StdioFrameBytes:       maxStdioJSONFrameBytes,
	}
}
