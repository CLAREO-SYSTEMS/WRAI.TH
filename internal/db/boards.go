package db

import (
	"agent-relay/internal/models"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (d *DB) CreateBoard(project, name, slug, description, createdBy string) (*models.Board, error) {
	now := time.Now().UTC().Format(memoryTimeFmt)
	b := &models.Board{
		ID:          uuid.New().String(),
		Project:     project,
		Name:        name,
		Slug:        slug,
		Description: description,
		CreatedBy:   createdBy,
		CreatedAt:   now,
	}

	_, err := d.conn.Exec(
		`INSERT INTO boards (id, project, name, slug, description, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.Project, b.Name, b.Slug, b.Description, b.CreatedBy, b.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create board: %w", err)
	}
	return b, nil
}

func (d *DB) ListBoards(project string) ([]models.Board, error) {
	rows, err := d.conn.Query(
		`SELECT id, project, name, slug, description, created_by, created_at FROM boards WHERE project = ? ORDER BY created_at`,
		project,
	)
	if err != nil {
		return nil, fmt.Errorf("list boards: %w", err)
	}
	defer rows.Close()

	var boards []models.Board
	for rows.Next() {
		var b models.Board
		if err := rows.Scan(&b.ID, &b.Project, &b.Name, &b.Slug, &b.Description, &b.CreatedBy, &b.CreatedAt); err != nil {
			return nil, err
		}
		boards = append(boards, b)
	}
	return boards, rows.Err()
}

func (d *DB) ListAllBoards() ([]models.Board, error) {
	rows, err := d.conn.Query(
		`SELECT id, project, name, slug, description, created_by, created_at FROM boards ORDER BY project, created_at`,
	)
	if err != nil {
		return nil, fmt.Errorf("list all boards: %w", err)
	}
	defer rows.Close()

	var boards []models.Board
	for rows.Next() {
		var b models.Board
		if err := rows.Scan(&b.ID, &b.Project, &b.Name, &b.Slug, &b.Description, &b.CreatedBy, &b.CreatedAt); err != nil {
			return nil, err
		}
		boards = append(boards, b)
	}
	return boards, rows.Err()
}

func (d *DB) GetBoard(project, slug string) (*models.Board, error) {
	var b models.Board
	err := d.conn.QueryRow(
		`SELECT id, project, name, slug, description, created_by, created_at FROM boards WHERE project = ? AND slug = ?`,
		project, slug,
	).Scan(&b.ID, &b.Project, &b.Name, &b.Slug, &b.Description, &b.CreatedBy, &b.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get board: %w", err)
	}
	return &b, nil
}
