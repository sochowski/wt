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
