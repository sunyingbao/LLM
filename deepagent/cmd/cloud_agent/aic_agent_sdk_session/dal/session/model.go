package session

const (
	StatusActive   int64 = 1
	StatusArchived int64 = 2
	StatusClosed   int64 = 3
)

type Session struct {
	SessionID          int64
	UID                int64
	ProjectName        string
	ProjectPath        string
	Title              string
	Status             int64
	MainThreadID       int64
	LastMessagePreview string
	LastActiveAtMS     int64
	CreatedAtMS        int64
	UpdatedAtMS        int64
	ClosedAtMS         int64
	MetadataJSON       string
}

type ListFilter struct {
	UID         int64
	ProjectName string
	Status      *int64
	Cursor      *Cursor
	Limit       int
}

type SessionProject struct {
	ProjectName    string
	ProjectPath    string
	SessionCount   int64
	LastActiveAtMS int64
}

type Cursor struct {
	LastActiveAtMS int64
	UpdatedAtMS    int64
	SessionID      int64
}

type UpdatePatch struct {
	Title     *string
	Status    *int64
	UpdatedAt int64
}

type TouchPatch struct {
	LastMessagePreview *string
	TitleIfEmpty       *string
	LastActiveAtMS     int64
	UpdatedAtMS        int64
}
