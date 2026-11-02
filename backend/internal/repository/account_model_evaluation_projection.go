package repository

import (
	"fmt"

	entsql "entgo.io/ent/dialect/sql"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
)

// Strip large answer/SVG bodies inside PostgreSQL, before the account row is
// transferred and JSON-decoded in Go. Preserve every unrelated extra field and
// each project's latest verdict in original newest-first history order.
func accountListEvaluationExtraSQL(extra string) string {
	value := "COALESCE(" + extra + ", '{}'::jsonb)"
	return fmt.Sprintf(`CASE WHEN jsonb_typeof(%[1]s->'model_evaluation_results') = 'array' THEN
		(%[1]s - 'model_evaluation_results') || jsonb_build_object('model_evaluation_results', (
			SELECT COALESCE(jsonb_agg(latest.summary ORDER BY latest.ordinality), '[]'::jsonb)
			FROM (
				SELECT DISTINCT ON (recent.entry->>'kind')
					(recent.entry - 'answer' - 'svg') || '{"answer":""}'::jsonb AS summary, recent.ordinality
				FROM (
					SELECT value AS entry, ordinality
					FROM jsonb_array_elements(%[1]s->'model_evaluation_results') WITH ORDINALITY history(value, ordinality)
					ORDER BY ordinality LIMIT 5
				) recent
				WHERE jsonb_typeof(recent.entry) = 'object' AND recent.entry->>'kind' IN ('candy', 'pelican')
				ORDER BY recent.entry->>'kind', recent.ordinality
			) latest
		))
	ELSE %[1]s - 'model_evaluation_results' END`, value)
}

func projectAccountListEvaluationExtra(selector *entsql.Selector) {
	columns := selector.SelectedColumns()
	extra := selector.C(dbaccount.FieldExtra)
	selector.Select()
	for _, column := range columns {
		if column == extra || column == dbaccount.FieldExtra {
			selector.AppendSelectExprAs(entsql.Expr(accountListEvaluationExtraSQL(extra)), dbaccount.FieldExtra)
		} else {
			selector.AppendSelect(column)
		}
	}
}
