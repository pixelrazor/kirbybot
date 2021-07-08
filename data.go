package main

import (
	"sync"

	"github.com/boltdb/bolt"
)

type ConfigRepository interface {
	SetKirbChannel(guildID, channelID string)
	RemoveKirbChannel(guildID string)
	GetKirbChannels() map[string]string
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

func (br *BoltRepo) SetKirbChannel(guildID, channelID string) {
	br.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(kirbPostingBucket)
		b.Put([]byte(guildID), []byte(channelID))
		return nil
	})
}

func (br *BoltRepo) RemoveKirbChannel(guildID string) {
	br.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(kirbPostingBucket)
		b.Delete([]byte(guildID))
		return nil
	})
}

func (br *BoltRepo) GetKirbChannels() map[string]string {
	channels := make(map[string]string)
	br.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(kirbPostingBucket)
		b.ForEach(func(k, v []byte) error {
			channels[string(k)] = string(v)
			return nil
		})
		return nil
	})
	return channels
}

type MapRepo struct {
	sync.RWMutex
	channels map[string]string
}

func NewMapRepo() *MapRepo {
	return &MapRepo{channels: make(map[string]string)}
}

func (mr *MapRepo) SetKirbChannel(guildID, channelID string) {
	mr.Lock()
	defer mr.Unlock()
	mr.channels[guildID] = channelID
}

func (mr *MapRepo) RemoveKirbChannel(guildID string) {
	mr.Lock()
	defer mr.Unlock()
	delete(mr.channels, guildID)
}

func (mr *MapRepo) GetKirbChannels() map[string]string {
	mr.RLock()
	defer mr.RUnlock()
	channels := make(map[string]string)
	for k, v := range mr.channels {
		channels[k] = v
	}
	return channels
}
