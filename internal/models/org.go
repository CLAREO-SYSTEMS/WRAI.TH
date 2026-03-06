package models

type Org struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

type Team struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Slug         string  `json:"slug"`
	OrgID        *string `json:"org_id,omitempty"`
	Project      string  `json:"project"`
	Description  string  `json:"description"`
	Type         string  `json:"type"` // regular | admin | bot
	ParentTeamID *string `json:"parent_team_id,omitempty"`
	CreatedAt    string  `json:"created_at"`
}

type TeamMember struct {
	TeamID    string  `json:"team_id"`
	AgentName string  `json:"agent_name"`
	Project   string  `json:"project"`
	Role      string  `json:"role"` // admin | lead | member | observer
	JoinedAt  string  `json:"joined_at"`
	LeftAt    *string `json:"left_at,omitempty"`
}

type TeamWithMembers struct {
	Team    Team         `json:"team"`
	Members []TeamMember `json:"members"`
}
