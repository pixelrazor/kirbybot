package main

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"

	_ "github.com/lib/pq"
)

type ConfigRepository interface {
	SetKirbChannel(guildID, channelID string) error
	RemoveKirbChannel(guildID string) error
	GetKirbChannels() (map[string]string, error)
}

type MapRepo struct {
	sync.RWMutex
	channels map[string]string
}

func NewMapRepo() *MapRepo {
	return &MapRepo{channels: make(map[string]string)}
}

func (mr *MapRepo) SetKirbChannel(guildID, channelID string) error {
	mr.Lock()
	defer mr.Unlock()
	mr.channels[guildID] = channelID
	return nil
}

func (mr *MapRepo) RemoveKirbChannel(guildID string) error {
	mr.Lock()
	defer mr.Unlock()
	delete(mr.channels, guildID)
	return nil
}

func (mr *MapRepo) GetKirbChannels() (map[string]string, error) {
	mr.RLock()
	defer mr.RUnlock()
	channels := make(map[string]string)
	for k, v := range mr.channels {
		channels[k] = v
	}
	return channels, nil
}

type PostgresRepo struct {
	db *sql.DB
}

func NewPostgresRepo(host, dbname, user, password string) (*PostgresRepo, error) {
	db, err := sql.Open("postgres", fmt.Sprintf("host=%v dbname=%v user=%v password=%v sslmode=disable", host, dbname, user, password))
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	return &PostgresRepo{db: db}, nil
}

func (pr *PostgresRepo) Close() error {
	return pr.db.Close()
}

func (pr *PostgresRepo) SetKirbChannel(guildID, channelID string) error {
	_, err := pr.db.Exec("INSERT INTO kirby_channels (guild_id, channel_id) VALUES ($1, $2) ON CONFLICT (guild_id) DO UPDATE SET channel_id = EXCLUDED.channel_id", guildID, channelID)
	return err
}

func (pr *PostgresRepo) RemoveKirbChannel(guildID string) error {
	_, err := pr.db.Exec("DELETE FROM kirby_channels WHERE guild_id = $1", guildID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}
func (pr *PostgresRepo) GetKirbChannels() (map[string]string, error) {
	channels := make(map[string]string)
	rows, err := pr.db.Query("SELECT guild_id, channel_id FROM kirby_channels")
	if err != nil {
		return nil, fmt.Errorf("failed to query table: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var guild, channel string
		err := rows.Scan(&guild, &channel)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		channels[guild] = channel
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error during row iteration: %w", err)
	}
	return channels, nil
}
