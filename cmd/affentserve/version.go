package main

var (
	buildRevision = "unknown"
	buildDate     = "unknown"
)

type buildInfo struct {
	Revision string `json:"build_revision"`
	Date     string `json:"build_date"`
}

func currentBuildInfo() buildInfo {
	return buildInfo{
		Revision: buildRevision,
		Date:     buildDate,
	}
}
