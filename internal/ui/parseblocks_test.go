package ui

import (
	"reflect"
	"testing"

	"github.com/Townk/ai-playbook/internal/playbook"
)

// blockCorpus is the shared fixture corpus pinning the block-parse contract
// (ADR-0009 step 1). It exercises every classification branch
// (shell/run/diff/create/static), the NormalizeFences repair path, auto-id
// synthesis, needs=/rollback= attributes, indented code blocks, and the
// top-level-only rule (code nested in a list/blockquote is NOT a block). Both
// TestCountBlocksMatchesRender and TestParseBlocksMatchesRender consume it.
var blockCorpus = []string{
	"",
	"# Title\n\nJust prose, no blocks.\n",
	"# Title\n\n```go {id=a}\nfunc main() {}\n```\n\n```python {id=b}\nprint('x')\n```\n\n```\nplain\n```\n",
	"# T\n\n```bash {id=s}\nls\n```\n\n```diff {id=d}\n- a\n+ b\n```\n\n```text {id=c file=x.txt}\nhi\n```\n",
	"# T\n\n```json {static}\n{\"k\":1}\n```\n\n```\nno lang\n```\n",
	// Malformed closing fence — NormalizeFences repairs it; both paths must agree.
	"# T\n\n```go {id=a}\nfmt.Println(1)\n```trailing text on the closer\n\nmore prose\n",
	// Indented (4-space) code block → a top-level *ast.CodeBlock.
	"# T\n\n    indented code block\n    second line\n\nprose\n",
	// Code fenced INSIDE a list item and a blockquote — Render walks these for
	// prose only, so neither becomes a Block.
	"# T\n\n- item with code:\n\n  ```go\n  nested()\n  ```\n\n> ```go\n> quoted()\n> ```\n",
	// needs= and rollback= attributes plus auto-id synthesis for an untagged block.
	"# T\n\n```bash {id=fix}\nmake fix\n```\n\n```bash {id=verify needs=fix}\nmake test\n```\n\n```bash {rollback=fix}\nmake clean\n```\n\n```bash\nuntagged\n```\n",
}

// TestParseBlocksMatchesRender pins ADR-0009 step 1: playbook.ParseBlocks is the
// SINGLE canonical block parser, and the styled renderer reports exactly the
// blocks it produces. Any drift between the pure parser and the renderer's block
// extraction — a field, the auto-id, the top-level-only rule — fails here.
func TestParseBlocksMatchesRender(t *testing.T) {
	for i, md := range blockCorpus {
		_, _, rendered := Render(md, 80, RenderOpts{})
		parsed := playbook.ParseBlocks(md)
		if !reflect.DeepEqual(parsed, rendered) {
			t.Errorf("corpus[%d]: ParseBlocks != Render blocks\n--- ParseBlocks ---\n%+v\n--- Render ---\n%+v\n--- md ---\n%s", i, parsed, rendered, md)
		}
	}
}
