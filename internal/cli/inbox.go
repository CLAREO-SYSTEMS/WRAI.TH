package cli

import (
	"fmt"
	"os"
)

func runInbox(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: agent-relay inbox <agent>")
		os.Exit(1)
	}

	agent := args[0]
	d := openDB()
	defer d.Close()

	messages, err := d.GetInbox(agent, true, 50)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(messages) == 0 {
		fmt.Printf("no unread messages for %s\n", agent)
		return
	}

	fmt.Printf("%d unread:\n", len(messages))
	for _, m := range messages {
		fmt.Printf("  [%s] %s → %q  (%s)  id:%s\n",
			m.Type,
			m.From,
			truncate(m.Subject, 50),
			timeAgo(m.CreatedAt),
			m.ID[:8],
		)
	}
}
