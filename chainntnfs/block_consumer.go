package chainntnfs

import (
	"sync"
	"sync/atomic"
)

type BlockHeightSyncer interface {
	BestBlockHeight() uint32
	consumerID() uint32
}

type syncer struct {
	parent *BlockConsumerCoordinator
	id     uint32
}

func (s *syncer) BestBlockHeight() uint32 {
	return s.parent.BestBlockHeight()
}

func (s *syncer) consumerID() uint32 {
	return s.id
}

type BlockConsumerCoordinator struct {
	consumerID   uint32 // To be used atomically.
	consumers    map[uint32]func(*BlockEpoch)
	consumersMtx sync.Mutex

	bestBlock    uint32
	bestBlockMtx sync.RWMutex
}

func (c *BlockConsumerCoordinator) BestBlockHeight() uint32 {
	c.bestBlockMtx.RLock()
	defer c.bestBlockMtx.RUnlock()

	return c.bestBlock
}

func (c *BlockConsumerCoordinator) RegisterConsumer(
	cb func(*BlockEpoch)) BlockHeightSyncer {

	c.consumersMtx.Lock()
	defer c.consumersMtx.Unlock()

	id := atomic.AddUint32(&c.consumerID, 1)
	c.consumers[id] = cb

	return &syncer{
		parent: c,
		id:     id,
	}
}

func (c *BlockConsumerCoordinator) RemoveConsumer(syncer BlockHeightSyncer) {
	c.consumersMtx.Lock()
	defer c.consumersMtx.Unlock()

	delete(c.consumers, syncer.consumerID())
}

func (c *BlockConsumerCoordinator) NotifyNewHeight(epoch *BlockEpoch) {
	c.bestBlockMtx.Lock()
	c.consumersMtx.Lock()

	defer c.bestBlockMtx.Unlock()
	defer c.consumersMtx.Unlock()

	for _, consumer := range c.consumers {
		consumer(epoch)
	}

	c.bestBlock = uint32(epoch.Height)
}
