// Package seed provides idempotent showcase seed data as Go migrations.
package seed

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/laenen-partners/entitystore"
	"github.com/laenen-partners/entitystore/matching"
	"github.com/laenen-partners/entitystore/store"
	"github.com/laenen-partners/migrate"
)

const scope = "explorer-seed"

// Run applies all pending seed migrations.
func Run(ctx context.Context, pool *pgxpool.Pool, es *entitystore.EntityStore) error {
	migrations := []migrate.GoMigration{
		{Version: "20260325000001", Name: "people_and_companies", Up: func(_ context.Context) error {
			return seedPeopleAndCompanies(ctx, es)
		}},
		{Version: "20260325000002", Name: "invoices_and_relations", Up: func(_ context.Context) error {
			return seedInvoicesAndRelations(ctx, es)
		}},
	}

	return migrate.UpGo(ctx, pool, migrations, scope)
}

func seedPeopleAndCompanies(ctx context.Context, es *entitystore.EntityStore) error {
	slog.Info("seeding people and companies")

	people := []map[string]any{
		{"full_name": "Alice Dupont", "email": "alice@dupont.be", "phone": "+32470123456", "job_title": "CEO", "date_of_birth": "1985-03-22"},
		{"full_name": "Bob Martin", "email": "bob@techcorp.io", "phone": "+32470987654", "job_title": "CTO", "date_of_birth": "1990-07-15"},
		{"full_name": "Charlie Peeters", "email": "charlie@design.co", "phone": "+32470555666", "job_title": "Lead Designer", "date_of_birth": "1988-11-03"},
		{"full_name": "Diana Janssen", "email": "diana@acme.com", "phone": "+32470111222", "job_title": "VP of Sales", "date_of_birth": "1982-01-17"},
		{"full_name": "Eve Laurent", "email": "eve@startup.io", "phone": "+32470333444", "job_title": "Founder", "date_of_birth": "1995-06-30"},
	}

	companies := []map[string]any{
		{"name": "Dupont & Partners", "domain": "dupont.be", "industry": "consulting", "founded": "2010"},
		{"name": "TechCorp", "domain": "techcorp.io", "industry": "technology", "founded": "2015"},
		{"name": "Design Co", "domain": "design.co", "industry": "design", "founded": "2018"},
		{"name": "Acme Inc", "domain": "acme.com", "industry": "manufacturing", "founded": "1998"},
	}

	for _, p := range people {
		data, _ := structpb.NewStruct(p)
		email := p["email"].(string)
		name := p["full_name"].(string)
		_, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
			{WriteEntity: &entitystore.WriteEntityOp{
				Action:     entitystore.WriteActionCreate,
				Data:       data,
				Confidence: 0.95,
				Tags:       []string{"ws:showcase", "type:person"},
				Anchors:    []matching.AnchorQuery{{Field: "email", Value: matching.NormalizeLowercaseTrim(email)}},
				Tokens:     map[string][]string{"full_name": matching.Tokenize(name)},
			}},
		})
		if err != nil {
			return err
		}
	}

	for _, c := range companies {
		data, _ := structpb.NewStruct(c)
		domain := c["domain"].(string)
		name := c["name"].(string)
		_, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
			{WriteEntity: &entitystore.WriteEntityOp{
				Action:     entitystore.WriteActionCreate,
				Data:       data,
				Confidence: 0.90,
				Tags:       []string{"ws:showcase", "type:company"},
				Anchors:    []matching.AnchorQuery{{Field: "domain", Value: matching.NormalizeLowercaseTrim(domain)}},
				Tokens:     map[string][]string{"name": matching.Tokenize(name)},
			}},
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func seedInvoicesAndRelations(ctx context.Context, es *entitystore.EntityStore) error {
	slog.Info("seeding invoices and relations")

	// Find seeded entities by anchor.
	alice, _ := es.GetByAnchor(ctx, "google.protobuf.Struct", "email", "alice@dupont.be", nil)
	bob, _ := es.GetByAnchor(ctx, "google.protobuf.Struct", "email", "bob@techcorp.io", nil)
	charlie, _ := es.GetByAnchor(ctx, "google.protobuf.Struct", "email", "charlie@design.co", nil)
	diana, _ := es.GetByAnchor(ctx, "google.protobuf.Struct", "email", "diana@acme.com", nil)
	eve, _ := es.GetByAnchor(ctx, "google.protobuf.Struct", "email", "eve@startup.io", nil)
	dupont, _ := es.GetByAnchor(ctx, "google.protobuf.Struct", "domain", "dupont.be", nil)
	techcorp, _ := es.GetByAnchor(ctx, "google.protobuf.Struct", "domain", "techcorp.io", nil)
	designco, _ := es.GetByAnchor(ctx, "google.protobuf.Struct", "domain", "design.co", nil)
	acme, _ := es.GetByAnchor(ctx, "google.protobuf.Struct", "domain", "acme.com", nil)

	// Create employment relations.
	relations := []struct {
		src, tgt   string
		relType    string
		confidence float64
		evidence   string
	}{
		{alice.ID, dupont.ID, "works_at", 0.95, "Founder and CEO of Dupont & Partners"},
		{bob.ID, techcorp.ID, "works_at", 0.92, "Listed as CTO on company website"},
		{charlie.ID, designco.ID, "works_at", 0.90, "LinkedIn profile"},
		{diana.ID, acme.ID, "works_at", 0.88, "Extracted from email signature"},
		{eve.ID, techcorp.ID, "works_at", 0.85, "Contract document"},
		{alice.ID, bob.ID, "knows", 0.80, "Co-authored blog post"},
		{bob.ID, charlie.ID, "knows", 0.75, "Attended same conference"},
		{alice.ID, diana.ID, "knows", 0.70, "Board meeting minutes"},
		{dupont.ID, acme.ID, "partner_of", 0.85, "Joint venture agreement"},
		{techcorp.ID, designco.ID, "client_of", 0.90, "Active contract"},
	}

	for _, r := range relations {
		if r.src == "" || r.tgt == "" {
			continue
		}
		_, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
			{UpsertRelation: &store.UpsertRelationOp{
				SourceID:     r.src,
				TargetID:     r.tgt,
				RelationType: r.relType,
				Confidence:   r.confidence,
				Evidence:     r.evidence,
			}},
		})
		if err != nil {
			return err
		}
	}

	// Create invoices.
	invoices := []struct {
		number string
		issuer string
		amount float64
		date   string
	}{
		{"INV-2024-001", "Dupont & Partners", 15000.00, "2024-01-15"},
		{"INV-2024-002", "TechCorp", 42000.00, "2024-02-20"},
		{"INV-2024-003", "Design Co", 8500.00, "2024-03-10"},
	}

	for _, inv := range invoices {
		data, _ := structpb.NewStruct(map[string]any{
			"invoice_number": inv.number,
			"issuer_name":    inv.issuer,
			"total_amount":   inv.amount,
			"invoice_date":   inv.date,
			"currency":       "EUR",
		})
		_, err := es.BatchWrite(ctx, []entitystore.BatchWriteOp{
			{WriteEntity: &entitystore.WriteEntityOp{
				Action:     entitystore.WriteActionCreate,
				Data:       data,
				Confidence: 0.92,
				Tags:       []string{"ws:showcase", "type:invoice"},
				Anchors:    []matching.AnchorQuery{{Field: "invoice_number", Value: matching.NormalizeLowercaseTrim(inv.number)}},
			}},
		})
		if err != nil {
			return err
		}
	}

	_ = time.Now() // seed timestamp reference

	return nil
}
