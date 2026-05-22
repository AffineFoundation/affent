package agent

import internalmemory "github.com/affinefoundation/affent/internal/memory"

// MemoryTarget selects which persistent store a memory tool call operates on.
type MemoryTarget = internalmemory.MemoryTarget

const (
	TargetMemory MemoryTarget = internalmemory.TargetMemory
	TargetUser   MemoryTarget = internalmemory.TargetUser
)

// MemoryStore is the abstraction the Loop uses to inject persistent memory
// and the `memory` tool uses to mutate / retrieve that state.
type MemoryStore = internalmemory.MemoryStore

// MemoryResponse is the memory tool's return shape.
type MemoryResponse = internalmemory.MemoryResponse

// MemorySearchResult is one ranked hit returned by Search.
type MemorySearchResult = internalmemory.MemorySearchResult

// MemoryTopicSummary is one row in a ListTopics response.
type MemoryTopicSummary = internalmemory.MemoryTopicSummary

// MemoryUsage carries capacity numbers for a single bucket.
type MemoryUsage = internalmemory.MemoryUsage

const (
	DefaultCoreCharLimit  = internalmemory.DefaultCoreCharLimit
	DefaultTopicCharLimit = internalmemory.DefaultTopicCharLimit
	DefaultUserCharLimit  = internalmemory.DefaultUserCharLimit

	CoreTopic    = internalmemory.CoreTopic
	DefaultTopic = internalmemory.DefaultTopic
)

// FileMemoryStore persists memory as workspace-scoped topic files plus a
// user-scoped profile file. The concrete implementation lives in
// internal/memory; this alias preserves the public affent API.
type FileMemoryStore = internalmemory.FileMemoryStore

// NewFileMemoryStore returns a FileMemoryStore wired to the standard workspace
// and user memory paths.
func NewFileMemoryStore(workspaceDir string) *FileMemoryStore {
	return internalmemory.NewFileMemoryStore(workspaceDir)
}
