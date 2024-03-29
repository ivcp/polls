package data

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgtype"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Poll struct {
	ID                string        `json:"id"`
	Question          string        `json:"question"`
	Description       string        `json:"description"`
	Options           []*PollOption `json:"options"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
	ExpiresAt         ExpiresAt     `json:"expires_at"`
	ResultsVisibility string        `json:"results_visibility"`
	IsPrivate         bool          `json:"is_private"`
	Token             string        `json:"token,omitempty"`
}

type PollModel struct {
	DB *pgxpool.Pool
}

func (p PollModel) Insert(poll *Poll, tokenHash []byte) error {
	query := `
		INSERT INTO polls (question, description, expires_at, results_visibility, is_private)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at;				
		`

	args := []any{
		poll.Question,
		poll.Description,
		poll.ExpiresAt.Time,
		poll.ResultsVisibility,
		poll.IsPrivate,
	}

	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	err := p.DB.QueryRow(
		ctx, query, args...,
	).Scan(&poll.ID, &poll.CreatedAt, &poll.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert poll: %w", err)
	}

	var queryOptionsString strings.Builder
	queryOptionsString.WriteString(
		"INSERT INTO poll_options (value, poll_id, position, vote_count) VALUES ",
	)
	values := make([]any, 0, len(poll.Options)*4)
	count := 1

	for i, opt := range poll.Options {
		var str string
		comma := ","
		if i == len(poll.Options)-1 {
			comma = ""
		}
		str = fmt.Sprintf(
			"($%d, $%d, $%d, $%d)%s ", count, count+1, count+2, count+3, comma,
		)
		queryOptionsString.WriteString(str)
		values = append(values, opt.Value, poll.ID, opt.Position, opt.VoteCount)
		count += 4
	}
	queryOptionsString.WriteString(" RETURNING id;")

	ctx, cancel = context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	rows, err := p.DB.Query(ctx, queryOptionsString.String(), values...)
	if err != nil {
		return fmt.Errorf("insert poll options: %w", err)
	}
	defer rows.Close()

	optionIndex := 0
	for rows.Next() {
		err := rows.Scan(&poll.Options[optionIndex].ID)
		if err != nil {
			return fmt.Errorf("scan option id: %w", err)
		}
		optionIndex++
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("insert poll options: %w", err)
	}

	queryToken := `
		INSERT INTO tokens (hash, poll_id)
		VALUES ($1, $2);
	`
	ctx, cancel = context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	_, err = p.DB.Exec(ctx, queryToken, tokenHash, poll.ID)

	return err
}

func (p PollModel) Get(id string) (*Poll, error) {
	if id == "" {
		return nil, ErrRecordNotFound
	}

	query := `
		SELECT p.id, p. question, p.description, p.created_at, 
		p.updated_at, p.expires_at, p.results_visibility, p.is_private,
		po.id, po.value, po.position
		FROM polls p
		JOIN poll_options po ON po.poll_id = p.id 
		WHERE p.id = $1;
	`

	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	rows, err := p.DB.Query(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("get poll: %w", err)
	}
	defer rows.Close()

	var poll Poll
	var options []*PollOption

	first := true
	for rows.Next() {

		var option PollOption

		switch {
		case first:
			err = rows.Scan(
				&poll.ID,
				&poll.Question,
				&poll.Description,
				&poll.CreatedAt,
				&poll.UpdatedAt,
				&poll.ExpiresAt.Time,
				&poll.ResultsVisibility,
				&poll.IsPrivate,
				&option.ID,
				&option.Value,
				&option.Position,
			)
		default:
			err = rows.Scan(
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				&option.ID,
				&option.Value,
				&option.Position,
			)
		}

		if err != nil {
			return nil, fmt.Errorf("get poll - scan: %w", err)
		}

		options = append(options, &option)
		first = false
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get poll: %w", err)
	}

	if len(options) == 0 {
		return nil, ErrRecordNotFound
	}

	poll.Options = options

	return &poll, nil
}

func (p PollModel) Update(poll *Poll) error {
	queryPoll := `
		UPDATE polls
		SET question = $1, description = $2, 
		expires_at = $3, updated_at = NOW()
		WHERE id = $4
		RETURNING updated_at;
	`

	args := []any{
		poll.Question,
		poll.Description,
		poll.ExpiresAt.Time,
		poll.ID,
	}

	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	return p.DB.QueryRow(ctx, queryPoll, args...).Scan(&poll.UpdatedAt)
}

func (p PollModel) Delete(id string) error {
	if id == "" {
		return ErrRecordNotFound
	}

	query := `
		DELETE FROM polls
		WHERE id = $1;
	`

	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	result, err := p.DB.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete poll: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrRecordNotFound
	}

	return nil
}

func (p PollModel) GetAll(search string, filters Filters) ([]*Poll, Metadata, error) {
	query := fmt.Sprintf(`
		SELECT count(*) OVER(), p.id, p.question, p.description, 
		p.created_at, p.updated_at, p.expires_at, p.results_visibility,
	    jsonb_agg(jsonb_build_object(
			'id', po.id, 'value', po.value, 'position', po.position
			)) AS options
		FROM polls p
		JOIN poll_options po ON po.poll_id = p.id 
		WHERE (to_tsvector('simple', question) @@ plainto_tsquery('simple', $1) OR $1 = '') 
		AND p.is_private = false
		GROUP BY p.id
		ORDER BY %s %s, id ASC
		LIMIT $2 OFFSET $3;
	`, filters.sortColumn(), filters.sortDirection())

	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()

	rows, err := p.DB.Query(ctx, query, search, filters.limit(), filters.offset())
	if err != nil {
		return nil, Metadata{}, fmt.Errorf("get all polls: %w", err)
	}
	defer rows.Close()

	var totalRecords int
	polls := []*Poll{}

	for rows.Next() {
		var poll Poll
		var optionsJson string
		err := rows.Scan(
			&totalRecords,
			&poll.ID,
			&poll.Question,
			&poll.Description,
			&poll.CreatedAt,
			&poll.UpdatedAt,
			&poll.ExpiresAt.Time,
			&poll.ResultsVisibility,
			&optionsJson,
		)
		if err != nil {
			return nil, Metadata{}, fmt.Errorf("get polls - scan: %w", err)
		}

		if err := json.Unmarshal([]byte(optionsJson), &poll.Options); err != nil {
			return nil, Metadata{}, fmt.Errorf("get polls - unmarshal options: %w", err)
		}
		polls = append(polls, &poll)
	}

	if err = rows.Err(); err != nil {
		return nil, Metadata{}, fmt.Errorf("get polls: %w", err)
	}

	metadata := calculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return polls, metadata, nil
}

func (p PollModel) GetVotedIPs(pollID string) ([]*net.IP, error) {
	query := `
		SELECT ip
		FROM ips
		WHERE poll_id = $1;
	`
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()
	rows, err := p.DB.Query(ctx, query, pollID)
	if err != nil {
		return nil, fmt.Errorf("get ips: %w", err)
	}
	defer rows.Close()

	var ips []*net.IP

	for rows.Next() {
		var ip pgtype.Inet
		err := rows.Scan(&ip)
		if err != nil {
			return nil, fmt.Errorf("get ips - scan: %w", err)
		}
		ips = append(ips, &ip.IPNet.IP)

	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get ips: %w", err)
	}

	return ips, nil
}

func (p PollModel) CheckToken(tokenPlaintext string) (string, error) {
	tokenHash := sha256.Sum256([]byte(tokenPlaintext))

	query := `
			SELECT poll_id
			FROM tokens
			WHERE hash = $1;
		`
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()
	row := p.DB.QueryRow(ctx, query, tokenHash[:])

	var pollID string
	err := row.Scan(&pollID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrRecordNotFound
		}
		return "", fmt.Errorf("check token: %w", err)
	}

	return pollID, nil
}
