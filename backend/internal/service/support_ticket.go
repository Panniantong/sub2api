package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 工单状态：open（待处理）/ answered（已回复）/ closed（已关闭）。
const (
	TicketStatusOpen     = "open"
	TicketStatusAnswered = "answered"
	TicketStatusClosed   = "closed"
)

// TicketReply 工单回复。
type TicketReply struct {
	ID        int64     `json:"id"`
	TicketID  int64     `json:"ticket_id"`
	Author    string    `json:"author"` // user / admin
	AuthorID  int64     `json:"author_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// SupportTicket 工单（含首层回复列表由详情接口返回）。
type SupportTicket struct {
	ID        int64         `json:"id"`
	UserID    int64         `json:"user_id"`
	UserEmail string        `json:"user_email,omitempty"`
	Subject   string        `json:"subject"`
	Status    string        `json:"status"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	ClosedAt  *time.Time    `json:"closed_at,omitempty"`
	Replies   []TicketReply `json:"replies,omitempty"`
}

var (
	ErrTicketNotFound = errors.New("support ticket not found")
	ErrTicketClosed   = errors.New("support ticket is closed")
)

// TicketService 轻量工单服务（SQL 直连，无 ent）。
type TicketService struct {
	db *sql.DB
}

// NewTicketService 创建工单服务。
func NewTicketService(db *sql.DB) *TicketService {
	return &TicketService{db: db}
}

func validateTicketSubject(subject string) (string, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", infraerrors.BadRequest("TICKET_SUBJECT_REQUIRED", "subject must not be empty")
	}
	if len([]rune(subject)) > 200 {
		return "", infraerrors.BadRequest("TICKET_SUBJECT_TOO_LONG", "subject must not exceed 200 runes")
	}
	return subject, nil
}

func validateTicketBody(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", infraerrors.BadRequest("TICKET_BODY_REQUIRED", "body must not be empty")
	}
	if len([]rune(body)) > 20000 {
		return "", infraerrors.BadRequest("TICKET_BODY_TOO_LONG", "body must not exceed 20000 runes")
	}
	return body, nil
}

// Create 用户建单（含首条描述），状态 open。
func (s *TicketService) Create(ctx context.Context, userID int64, subject, body string) (*SupportTicket, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("ticket service unavailable")
	}
	subject, err := validateTicketSubject(subject)
	if err != nil {
		return nil, err
	}
	body, err = validateTicketBody(body)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var t SupportTicket
	var closed sql.NullTime
	err = tx.QueryRowContext(ctx, `
		INSERT INTO support_tickets (user_id, subject, status, created_at, updated_at)
		VALUES ($1, $2, 'open', NOW(), NOW())
		RETURNING id, user_id, subject, status, created_at, updated_at, closed_at`,
		userID, subject).Scan(&t.ID, &t.UserID, &t.Subject, &t.Status, &t.CreatedAt, &t.UpdatedAt, &closed)
	if err != nil {
		return nil, err
	}
	if closed.Valid {
		t.ClosedAt = &closed.Time
	}
	var r TicketReply
	err = tx.QueryRowContext(ctx, `
		INSERT INTO support_ticket_replies (ticket_id, author_type, author_id, body, created_at)
		VALUES ($1, 'user', $2, $3, NOW())
		RETURNING id, ticket_id, author_type, author_id, body, created_at`,
		t.ID, userID, body).Scan(&r.ID, &r.TicketID, &r.Author, &r.AuthorID, &r.Body, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	t.Replies = []TicketReply{r}
	return &t, nil
}

func scanTicketRow(scan func(dest ...any) error) (*SupportTicket, error) {
	var t SupportTicket
	var closed sql.NullTime
	if err := scan(&t.ID, &t.UserID, &t.Subject, &t.Status, &t.CreatedAt, &t.UpdatedAt, &closed); err != nil {
		return nil, err
	}
	if closed.Valid {
		t.ClosedAt = &closed.Time
	}
	return &t, nil
}

// escapeTicketLike 转义 LIKE 通配符（% _ \），防搜索词改变语义。
func escapeTicketLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

const ticketColumns = `id, user_id, subject, status, created_at, updated_at, closed_at`

// ListMine 用户工单列表（倒序，limit 上限 200）。
func (s *TicketService) ListMine(ctx context.Context, userID int64) ([]SupportTicket, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("ticket service unavailable")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+ticketColumns+` FROM support_tickets
		WHERE user_id = $1 ORDER BY id DESC LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]SupportTicket, 0)
	for rows.Next() {
		t, err := scanTicketRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// ListAll 管理端列表：status 可选过滤，search 匹配主题/用户邮箱，offset 分页。
func (s *TicketService) ListAll(ctx context.Context, status, search string, limit, offset int) ([]SupportTicket, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("ticket service unavailable")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	q := `
		SELECT t.id, t.user_id, t.subject, t.status, t.created_at, t.updated_at, t.closed_at,
			COALESCE(u.email, '')
		FROM support_tickets t
		LEFT JOIN users u ON u.id = t.user_id
		WHERE ($1 = '' OR t.status = $1)
		  AND ($2 = '' OR t.subject ILIKE '%' || $2 || '%' ESCAPE '\' OR COALESCE(u.email, '') ILIKE '%' || $2 || '%' ESCAPE '\')
		ORDER BY t.id DESC LIMIT $3 OFFSET $4`
	rows, err := s.db.QueryContext(ctx, q, strings.TrimSpace(status), escapeTicketLike(strings.TrimSpace(search)), limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]SupportTicket, 0)
	for rows.Next() {
		var t SupportTicket
		if err := rows.Scan(&t.ID, &t.UserID, &t.Subject, &t.Status, &t.CreatedAt, &t.UpdatedAt, &t.ClosedAt, &t.UserEmail); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetMine 用户查看自己的工单（含回复）。
func (s *TicketService) GetMine(ctx context.Context, userID, id int64) (*SupportTicket, error) {
	t, err := s.getByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.UserID != userID {
		return nil, ErrTicketNotFound
	}
	return s.withReplies(ctx, t)
}

// GetAny 管理端查看任意工单（含回复与用户邮箱）。
func (s *TicketService) GetAny(ctx context.Context, id int64) (*SupportTicket, error) {
	t, err := s.getByID(ctx, id)
	if err != nil {
		return nil, err
	}
	var email string
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(email,'') FROM users WHERE id = $1`, t.UserID).Scan(&email)
	t.UserEmail = email
	return s.withReplies(ctx, t)
}

func (s *TicketService) getByID(ctx context.Context, id int64) (*SupportTicket, error) {
	var t SupportTicket
	var closed sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT `+ticketColumns+` FROM support_tickets WHERE id = $1`, id).
		Scan(&t.ID, &t.UserID, &t.Subject, &t.Status, &t.CreatedAt, &t.UpdatedAt, &closed)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTicketNotFound
		}
		return nil, err
	}
	if closed.Valid {
		t.ClosedAt = &closed.Time
	}
	return &t, nil
}

func (s *TicketService) withReplies(ctx context.Context, t *SupportTicket) (*SupportTicket, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ticket_id, author_type, author_id, body, created_at
		FROM support_ticket_replies WHERE ticket_id = $1 ORDER BY id`, t.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	t.Replies = make([]TicketReply, 0)
	for rows.Next() {
		var r TicketReply
		if err := rows.Scan(&r.ID, &r.TicketID, &r.Author, &r.AuthorID, &r.Body, &r.CreatedAt); err != nil {
			return nil, err
		}
		t.Replies = append(t.Replies, r)
	}
	return t, rows.Err()
}

// ReplyMine 用户追问（关闭后不可再回；追问后状态回到 open 待处理）。
func (s *TicketService) ReplyMine(ctx context.Context, userID, id int64, body string) (*TicketReply, error) {
	t, err := s.GetMine(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return s.addReply(ctx, t, "user", userID, body, TicketStatusOpen)
}

// ReplyAny 管理员回复（状态置 answered）。
func (s *TicketService) ReplyAny(ctx context.Context, adminID, id int64, body string) (*TicketReply, error) {
	t, err := s.getByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.addReply(ctx, t, "admin", adminID, body, TicketStatusAnswered)
}

func (s *TicketService) addReply(ctx context.Context, t *SupportTicket, author string, authorID int64, body, nextStatus string) (*TicketReply, error) {
	if t.Status == TicketStatusClosed {
		return nil, ErrTicketClosed
	}
	body, err := validateTicketBody(body)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var r TicketReply
	err = tx.QueryRowContext(ctx, `
		INSERT INTO support_ticket_replies (ticket_id, author_type, author_id, body, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING id, ticket_id, author_type, author_id, body, created_at`,
		t.ID, author, authorID, body).Scan(&r.ID, &r.TicketID, &r.Author, &r.AuthorID, &r.Body, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE support_tickets SET status = $2, updated_at = NOW(), closed_at = NULL WHERE id = $1`,
		t.ID, nextStatus); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &r, nil
}

// CloseMine 用户关闭自己的工单。
func (s *TicketService) CloseMine(ctx context.Context, userID, id int64) error {
	t, err := s.GetMine(ctx, userID, id)
	if err != nil {
		return err
	}
	return s.close(ctx, t.ID)
}

// CloseAny 管理员关闭任意工单。
func (s *TicketService) CloseAny(ctx context.Context, id int64) error {
	t, err := s.getByID(ctx, id)
	if err != nil {
		return err
	}
	return s.close(ctx, t.ID)
}

func (s *TicketService) close(ctx context.Context, id int64) error {
	// 已关闭不再刷新 closed_at（幂等）。
	_, err := s.db.ExecContext(ctx, `
		UPDATE support_tickets SET status = 'closed', updated_at = NOW(), closed_at = COALESCE(closed_at, NOW())
		WHERE id = $1 AND status <> 'closed'`, id)
	return err
}

// OpenCount 未关闭工单数（管理端角标）。
func (s *TicketService) OpenCount(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	var n int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM support_tickets WHERE status <> 'closed'`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
