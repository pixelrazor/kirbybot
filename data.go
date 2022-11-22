package main

import (
	"fmt"
	"sync"

	"github.com/boltdb/bolt"
)

type ConfigRepository interface {
	SetKirbChannel(guildID, channelID string) error
	RemoveKirbChannel(guildID string) error
	GetKirbChannels() (map[string]string, error)
}

type BoltRepo struct {
	db *bolt.DB
}

func NewBoltRepo(db *bolt.DB) *BoltRepo {
	db.Update(func(tx *bolt.Tx) error {
		tx.CreateBucketIfNotExists([]byte(kirbPostingBucket))
		return nil
	})
	return &BoltRepo{db: db}
}

var kirbPostingBucket = []byte("kirb-posting") // server ID -> channel ID

func (br *BoltRepo) SetKirbChannel(guildID, channelID string) error {
	return br.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(kirbPostingBucket)
		return b.Put([]byte(guildID), []byte(channelID))
	})
}

func (br *BoltRepo) RemoveKirbChannel(guildID string) error {
	return br.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(kirbPostingBucket)
		return b.Delete([]byte(guildID))
	})
}

func (br *BoltRepo) GetKirbChannels() (map[string]string, error) {
	channels := make(map[string]string)
	err := br.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(kirbPostingBucket)
		return b.ForEach(func(k, v []byte) error {
			channels[string(k)] = string(v)
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read kirb channels from boltdb: %w", err)
	}
	return channels, nil
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
