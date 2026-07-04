// Package draft defines the AI-layer submit-time playbook DTO: the structured
// schema the authoring model fills via the submit_playbook MCP tool, plus the
// deterministic markdown renderer and submit-time semantic validation. The
// model returns DATA; we render the markdown, so structure (H1, sections,
// block tags) cannot drift. This is an internal AI-layer surface — the public,
// harness-agnostic schema owner is pkg/playbook (see ADR-0009).
package draft

// Playbook is the whole document. Title is the playbook name (rendered as the H1
// "# <Title>" and used as the saved front-matter name). Front matter other than
// the title is carried in Meta and assembled at save time — Render emits the BODY
// only.
type Playbook struct {
	Title    string    `json:"title" jsonschema:"the playbook name; rendered as the H1 title. A short imperative phrase, e.g. 'Restore the Gradle wrapper'."`
	Intro    string    `json:"intro,omitempty" jsonschema:"optional lead prose before the first section (markdown)"`
	Sections []Section `json:"sections" jsonschema:"the ordered sections of the playbook; at least one"`
	Verify   *Step     `json:"verify,omitempty" jsonschema:"the final outcome-check command, rendered as the {id=verify} block. Include it for a troubleshooting/fix playbook."`
	Meta     Meta      `json:"meta" jsonschema:"classification + front-matter metadata for the saved playbook"`
}

// Section is a titled run of heterogeneous content.
type Section struct {
	Heading string        `json:"heading" jsonschema:"the section heading, rendered as '## <heading>'"`
	Content []ContentItem `json:"content" jsonschema:"ordered, heterogeneous list of prose and code items; render in order"`
}

// ContentItem is one prose or code element. Kind discriminates which fields apply
// (a flat discriminator, not a oneOf, for tool-use reliability).
type ContentItem struct {
	Kind       string   `json:"kind" jsonschema:"one of: text, callout, code"`
	Text       string   `json:"text,omitempty" jsonschema:"for kind=text or kind=callout: literate markdown prose"`
	Admonition string   `json:"admonition,omitempty" jsonschema:"for kind=callout: the callout type — one of note|tip|important|warning|caution (default note); selects the icon + color"`
	Lang       string   `json:"lang,omitempty" jsonschema:"for kind=code: the language/interpreter — bash|zsh|sh|python|diff|console|…"`
	Code       string   `json:"code,omitempty" jsonschema:"for kind=code: the block content"`
	ID         string   `json:"id,omitempty" jsonschema:"for kind=code: optional stable id for value-passing; we auto-assign when omitted"`
	Needs      []string `json:"needs,omitempty" jsonschema:"for kind=code: ids of earlier blocks this one depends on"`
	Rollback   string   `json:"rollback,omitempty" jsonschema:"for kind=code: the id of the block this one rolls back"`
	Static     bool     `json:"static,omitempty" jsonschema:"for kind=code: true if the block is non-runnable (console output / illustrative)"`
	File       string   `json:"file,omitempty" jsonschema:"for a NEW file: the relative path; the block body is the file's full content (use a diff block to EDIT an existing file)"`
	From       string   `json:"from,omitempty" jsonschema:"for kind=code: id of an earlier shell/run block whose captured stdout feeds this block's stdin (e.g. a python block reading sys.stdin); implies a needs= dependency on that id; only shell/run blocks may set this, and only a shell/run block may be the target"`
}

// Step is a single command used for the top-level verify.
type Step struct {
	Lang  string   `json:"lang" jsonschema:"the language/interpreter for the verify command"`
	Code  string   `json:"code" jsonschema:"the verify command content"`
	Needs []string `json:"needs,omitempty" jsonschema:"ids the verify depends on (usually the fix block)"`
	From  string   `json:"from,omitempty" jsonschema:"id of an earlier shell/run block whose captured stdout feeds the verify command's stdin; same rules as a code block's from="`
}

// Meta carries the classification + provenance fields folded into the same call
// (replacing the separate metadata model pass). The playbook NAME is the Title
// (the H1), so Meta has no name field.
type Meta struct {
	Description  string   `json:"description" jsonschema:"a one-line imperative summary of what the playbook does"`
	Category     string   `json:"category,omitempty" jsonschema:"a coarse category, e.g. 'Android / build' or 'macOS / networking'"`
	Tags         []string `json:"tags,omitempty" jsonschema:"keywords for search"`
	ProjectBound bool     `json:"project_bound" jsonschema:"true if this playbook is specific to a project/working directory; false for a general how-to that applies anywhere"`
	Env          []EnvVar `json:"env,omitempty" jsonschema:"environment variables the playbook relies on (local resources, secrets) — declare each with name + why so a reader on another machine knows what to set"`
}

// EnvVar is one declared environment variable the playbook relies on.
type EnvVar struct {
	Name string `json:"name" jsonschema:"the variable name, e.g. ANDROID_SDK_ROOT"`
	Why  string `json:"why,omitempty" jsonschema:"one line on what it is / why the playbook needs it"`
}
