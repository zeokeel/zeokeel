package main

import (
	"fmt"
	"strings"
)

// richAnswer is one turn that exercises the markdown renderer's whole
// vocabulary: headings, a list, a table, inline code, a fenced block with a
// language, a blockquote and a link.
const richAnswer = `## Result

The fixture streamed **bold**, *italic*, ` + "`inline code`" + ` and a [link](https://example.com).

- first item
- second item
  - nested item

| column | value |
| --- | --- |
| alpha | 1 |
| beta | 2 |

> A blockquote, because it has its own border and spacing.

` + "```go" + `
func main() {
	println("hello")
}
` + "```" + `

Done.`

// longAnswer is ~120 short lines: enough that the transcript has to scroll and
// window, and that a naive renderer shows its cost.
func longAnswer() string {
	var builder strings.Builder
	builder.WriteString("## Long answer\n\n")
	for index := 1; index <= 120; index++ {
		fmt.Fprintf(&builder, "%d. line %d of the long fixture — padding text for scrolling.\n", index, index)
	}
	return builder.String()
}
