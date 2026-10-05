package main

import (
	"database/sql"
	"errors"
)

func liveActorTx(tx *sql.Tx, root, actor, runtime string) error {
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM agent_sessions WHERE root_id=? AND id=? AND runtime=? AND runtime<>'' AND stopped=0`, root, actor, runtime).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return errors.New("stale actor runtime")
	}
	return nil
}

// Durable retries reconcile by stable operation ID. Only first pending claim
// spends a permit; claimed/uncertain reservations survive process replacement.
// This is intentionally separate from ordinary Pi's uncertain-delivery policy.
func (s *Store) claimDurableInbox(root, actor, runtime, id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = liveActorTx(tx, root, actor, runtime); err != nil {
		return err
	}
	var backend string
	if err = tx.QueryRow(`SELECT coalesce(json_extract(adapter,'$.backend'),'') FROM agent_sessions WHERE root_id=? AND id=?`, root, actor).Scan(&backend); err != nil {
		return err
	}
	if backend != "durable" {
		return errors.New("durable claim requires durable recipient")
	}
	var state string
	var request bool
	if err = tx.QueryRow(`SELECT state,request FROM inbox WHERE root_id=? AND recipient=? AND id=?`, root, actor, id).Scan(&state, &request); err != nil {
		return err
	}
	if state == "delivered" {
		return tx.Commit()
	}
	if state == "pending" && request {
		res, e := tx.Exec(`UPDATE roots SET wake_budget=wake_budget-1 WHERE id=? AND wake_enabled=1 AND wake_budget>0`, root)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return errors.New("automatic wake disabled or budget exhausted")
		}
	}
	if _, err = tx.Exec(`UPDATE inbox SET state='claimed',runtime=? WHERE root_id=? AND recipient=? AND id=?`, runtime, root, actor, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) recoverInbox(root, actor, runtime string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = liveActorTx(tx, root, actor, runtime); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE inbox SET state='uncertain' WHERE root_id=? AND recipient=? AND state='claimed'`, root, actor); err != nil {
		return err
	}
	return tx.Commit()
}

// Inbox pages exclude delivered bodies and cap even worst-case JSON escaping
// below the extension's 1 MiB subprocess limit. Cursor is the immutable rowid.
type InboxPage struct {
	Messages []InboxMessage `json:"messages"`
	Next     int64          `json:"next"`
}

func (s *Store) inboxPage(root, recipient string, after int64) (InboxPage, error) {
	out := InboxPage{Messages: []InboxMessage{}}
	rows, err := s.db.Query(`SELECT rowid,sender_kind,request,id,root_id,coalesce(sender,'human'),recipient,body,state,runtime FROM inbox WHERE root_id=? AND recipient=? AND state<>'delivered' AND rowid>? ORDER BY rowid LIMIT 8`, root, recipient, after)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var m InboxMessage
		if err = rows.Scan(&out.Next, &m.SenderKind, &m.Request, &m.ID, &m.RootID, &m.Sender, &m.Recipient, &m.Body, &m.State, &m.Runtime); err != nil {
			return out, err
		}
		out.Messages = append(out.Messages, m)
	}
	return out, rows.Err()
}
