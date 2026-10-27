package oms

// crew_members.go is crew MEMBERSHIP (migration 0173): which internal users make
// up a crew. A crew (crew.go) was a name with nobody behind it; this ties it to
// iam.users. The membership is the join everything downstream keys off -- a
// crew-assigned work order fans out to every member's field pack, and
// CrewsForUser answers "which crews is this user on" for that fan-out.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// Crew-member roles. Everyone is a 'member' by default; the admin promotes a
// 'lead'. Free text in the column, but these are the two the UI offers.
const (
	crewRoleMember = "member"
	crewRoleLead   = "lead"
)

// CrewMember mirrors oms.crew_members, enriched on read with the user's
// display_name/email (joined from iam.users) so the roster can show real names,
// not uuids. Role is free text (lead / member).
type CrewMember struct {
	bun.BaseModel `bun:"table:oms.crew_members,alias:cm"`

	ID      string    `bun:"id,pk" json:"id"`
	CrewID  string    `bun:"crew_id" json:"crew_id"`
	UserID  string    `bun:"user_id" json:"user_id"`
	Role    *string   `bun:"role" json:"role"`
	AddedAt time.Time `bun:"added_at" json:"added_at"`

	DisplayName *string `bun:"display_name,scanonly" json:"display_name"`
	Email       *string `bun:"email,scanonly" json:"email"`
}

// ListCrewMembers returns the users on a crew, each with their name/email,
// ordered by role then name (leads surface first).
func ListCrewMembers(ctx context.Context, db bun.IDB, crewID string) ([]CrewMember, error) {
	var out []CrewMember
	if err := db.NewSelect().Model(&out).
		ColumnExpr("cm.*").
		ColumnExpr("u.display_name AS display_name").
		ColumnExpr("u.email AS email").
		Join("LEFT JOIN iam.users AS u ON u.id = cm.user_id").
		Where("cm.crew_id = ?", crewID).
		OrderExpr("cm.role NULLS LAST, u.display_name").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("oms: listing crew %s members: %w", crewID, err)
	}
	return out, nil
}

// AddCrewMember adds a user to a crew (idempotent: re-adding updates the role).
// The crew must exist and be active; the user must exist and be active.
func AddCrewMember(ctx context.Context, db bun.IDB, crewID, userID string, role *string) (*CrewMember, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, invalidf("a user id is required")
	}
	crew := new(Crew)
	if err := db.NewSelect().Model(crew).Where("id = ?", crewID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("crew %q does not exist", crewID)
		}
		return nil, fmt.Errorf("oms: loading crew %s: %w", crewID, err)
	}
	if !crew.Active {
		return nil, invalidf("crew %q is retired", crewID)
	}
	ok, err := db.NewSelect().TableExpr("iam.users").
		Where("id = ?", userID).Where("status = ?", "active").Exists(ctx)
	if err != nil {
		return nil, fmt.Errorf("oms: checking user %s: %w", userID, err)
	}
	if !ok {
		return nil, invalidf("user %q does not exist or is not active", userID)
	}

	// Everyone is a member unless the admin promotes them to lead.
	memberRole := trimToNil(role)
	if memberRole == nil {
		def := crewRoleMember
		memberRole = &def
	}
	m := &CrewMember{CrewID: crewID, UserID: userID, Role: memberRole}
	if _, err := db.NewInsert().Model(m).
		Column("crew_id", "user_id", "role").
		On("CONFLICT (crew_id, user_id) DO UPDATE").
		Set("role = EXCLUDED.role").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("oms: adding member to crew %s: %w", crewID, err)
	}
	return getCrewMember(ctx, db, crewID, userID)
}

// RemoveCrewMember removes a user from a crew. Idempotent (removing a
// non-member is a no-op success).
func RemoveCrewMember(ctx context.Context, db bun.IDB, crewID, userID string) error {
	if _, err := db.NewDelete().Model((*CrewMember)(nil)).
		Where("crew_id = ?", crewID).Where("user_id = ?", userID).Exec(ctx); err != nil {
		return fmt.Errorf("oms: removing member from crew %s: %w", crewID, err)
	}
	return nil
}

// CrewsForUser returns the ids of the active crews a user is a member of. This is
// the field-pack fan-out lookup: a crew-assigned work order reaches a user if the
// order's crew is one of these.
func CrewsForUser(ctx context.Context, db bun.IDB, userID string) ([]string, error) {
	var ids []string
	if err := db.NewSelect().
		ColumnExpr("cm.crew_id::text").
		TableExpr("oms.crew_members AS cm").
		Join("JOIN oms.crews AS crew ON crew.id = cm.crew_id").
		Where("cm.user_id = ?", userID).
		Where("crew.active").
		Scan(ctx, &ids); err != nil {
		return nil, fmt.Errorf("oms: listing crews for user %s: %w", userID, err)
	}
	return ids, nil
}

func getCrewMember(ctx context.Context, db bun.IDB, crewID, userID string) (*CrewMember, error) {
	m := new(CrewMember)
	if err := db.NewSelect().Model(m).
		ColumnExpr("cm.*").
		ColumnExpr("u.display_name AS display_name").
		ColumnExpr("u.email AS email").
		Join("LEFT JOIN iam.users AS u ON u.id = cm.user_id").
		Where("cm.crew_id = ?", crewID).Where("cm.user_id = ?", userID).
		Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("user %q is not on crew %q", userID, crewID)
		}
		return nil, fmt.Errorf("oms: loading crew member: %w", err)
	}
	return m, nil
}
