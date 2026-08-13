package runtime

import "context"

const ThreadIndexSchemaVersion = 1

type ThreadIndexEntry struct {
	SchemaVersion     int             `json:"schema_version"`
	Ref               GlobalThreadRef `json:"ref"`
	DefinitionName    string          `json:"definition_name"`
	DefinitionVersion string          `json:"definition_version"`
	Workspace         WorkspaceSpec   `json:"workspace,omitempty"`
	Title             string          `json:"title,omitempty"`
	State             ThreadState     `json:"state,omitempty"`
	CreatedAtMS       int64           `json:"created_at_ms,omitempty"`
	UpdatedAtMS       int64           `json:"updated_at_ms,omitempty"`
	TimelineCursor    string          `json:"timeline_cursor,omitempty"`
}

type ThreadIndexQuery struct {
	Runtime   RuntimeKind
	Namespace string
}

type ThreadIndex interface {
	Put(ctx context.Context, entry ThreadIndexEntry) (err error)
	Get(ctx context.Context, ref GlobalThreadRef) (entry ThreadIndexEntry, err error)
	List(ctx context.Context, query ThreadIndexQuery) (entries []ThreadIndexEntry, err error)
}
