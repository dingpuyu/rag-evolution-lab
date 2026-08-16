package domain

import "time"

type Document struct {
	ID             string   `json:"doc_id"`
	Title          string   `json:"title"`
	DocType        string   `json:"doc_type"`
	Product        string   `json:"product"`
	Version        string   `json:"version"`
	Status         string   `json:"status"`
	EffectiveAt    string   `json:"effective_at"`
	ExpiresAt      *string  `json:"expires_at"`
	Language       string   `json:"language"`
	Visibility     string   `json:"visibility"`
	AllowedTenants []string `json:"allowed_tenants"`
	AllowedRoles   []string `json:"allowed_roles"`
	Source         string   `json:"source"`
	Quality        string   `json:"quality"`
	Path           string   `json:"path"`
	Content        string   `json:"-"`
}

type Chunk struct {
	ID                  string
	DocumentID          string
	DocumentTitle       string
	Content             string
	ParentID            string
	ParentContent       string
	ParentSequence      int
	SourcePage          int
	Sequence            int
	HeadingPath         []string
	DatasetID           string
	Domain              string
	Manufacturer        string
	ProductFamily       string
	ModelCodes          []string
	SoftwareVersionFrom string
	SoftwareVersionTo   string
	HardwareRevision    string
	Region              string
	Language            string
	EffectiveFrom       string
	EffectiveTo         string
	AuthorityLevel      string
	DocumentRevision    string
	Supersedes          []string
	SourceFile          string
	SourceSheet         string
	SourceCellRange     string
	DeviceIdentifiers   []string
	AffectedLots        []string
	Product             string
	Version             string
	Status              string
	Visibility          string
	AllowedTenants      []string
	AllowedRoles        []string
	Quality             string
}

type QueryRequest struct {
	Query     string
	Pipeline  string
	TenantID  string
	UserRole  string
	Product   string
	Version   string
	DatasetID string
	ModelCode string
	TopK      int
}

type RetrievedChunk struct {
	Chunk Chunk
	Score float64
	Rank  int
	Stage string
}

type Citation struct {
	ChunkID    string `json:"chunk_id"`
	DocumentID string `json:"document_id"`
	Document   string `json:"document"`
	Excerpt    string `json:"excerpt"`
}

type QueryResponse struct {
	Answer     string           `json:"answer"`
	Answerable bool             `json:"answerable"`
	Citations  []Citation       `json:"citations"`
	Retrieval  []RetrievedChunk `json:"-"`
	Context    []RetrievedChunk `json:"-"`
	Trace      QueryTrace       `json:"trace"`
}

type TraceEvent struct {
	Name       string         `json:"name"`
	StartedAt  time.Time      `json:"started_at"`
	DurationMS int64          `json:"duration_ms"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type QueryTrace struct {
	ID       string       `json:"id"`
	Pipeline string       `json:"pipeline"`
	Query    string       `json:"query"`
	Events   []TraceEvent `json:"events"`
	TotalMS  int64        `json:"total_ms"`
}

type GoldenCase struct {
	ID             string         `json:"id"`
	DatasetVersion string         `json:"dataset_version"`
	Split          string         `json:"split"`
	Category       string         `json:"category"`
	Query          string         `json:"query"`
	Context        GoldenContext  `json:"context"`
	Expected       GoldenExpected `json:"expected"`
	Notes          string         `json:"notes"`
}

type GoldenContext struct {
	TenantID string  `json:"tenant_id"`
	UserRole string  `json:"user_role"`
	Product  *string `json:"product"`
	Version  *string `json:"version"`
}

type GoldenExpected struct {
	Answerable            bool     `json:"answerable"`
	RelevantDocumentIDs   []string `json:"relevant_doc_ids"`
	RelevantChunkIDs      []string `json:"relevant_chunk_ids"`
	RequiredFacts         []string `json:"required_facts"`
	ForbiddenFacts        []string `json:"forbidden_facts"`
	MinimumCitations      int      `json:"minimum_citations"`
	ExpectedRefusalReason *string  `json:"expected_refusal_reason"`
}
