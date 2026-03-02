package cli

import "fmt"

func runAgents() {
	d := openDB()
	defer d.Close()

	agents, err := d.ListAgents()
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	if len(agents) == 0 {
		fmt.Println("no agents registered")
		return
	}

	rows := [][]string{{"NAME", "ROLE", "LAST SEEN"}}
	for _, a := range agents {
		rows = append(rows, []string{a.Name, truncate(a.Role, 30), timeAgo(a.LastSeen)})
	}
	printTable(rows)
}
