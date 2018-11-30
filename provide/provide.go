package provide

import (
	"context"
	"fmt"
	"gx/ipfs/QmR8BauakNcBa3RbE4nbQu76PDiJgoQgz8AJdhJuiU4TAw/go-cid"
	"gx/ipfs/QmZBH87CAPFHcc7cYmBqeSQ98zQ3SX9KUxiYgzPmLWNVKz/go-libp2p-routing"
	logging "gx/ipfs/QmcuXC5cxs79ro2cUuHs4HQ2bkDLJUYokwL8aivcX6HW3C/go-log"
	"time"
)

var (
	log = logging.Logger("provider")
)

const (
	provideOutgoingLimit 	= 512
	provideOutgoingTimeout  = time.Second * 15
)

type Strategy func(context.Context, chan cid.Cid, cid.Cid)

type Provider struct {
	ctx context.Context

	// cids we want to provide
	incoming chan cid.Cid
	// cids we are working on providing now
	outgoing chan cid.Cid

	// strategy for deciding which cids, given a cid, should be provided
	strategy Strategy

	contentRouting routing.ContentRouting // TODO: temp, maybe
}

func NewProvider(ctx context.Context, strategy Strategy, contentRouting routing.ContentRouting) *Provider {
	return &Provider{
		ctx: ctx,
		outgoing: make(chan cid.Cid),
		incoming: make(chan cid.Cid),
		strategy: strategy,
		contentRouting: contentRouting,
	}
}

// Start workers to handle provide requests.
func (p *Provider) Run() {
	go p.handleIncoming()
	go p.handleOutgoing()
}

// Provide the given cid using specified strategy.
func (p *Provider) Provide(root cid.Cid) {
	p.strategy(p.ctx, p.incoming, root)
}

// Announce to the world that a block is provided.
//
// TODO: Refactor duplication between here and the reprovider.
func (p *Provider) Announce(cid cid.Cid) {
	// TODO: REMOVE
	fmt.Println("starting announce", cid)
	ctx, cancel := context.WithTimeout(p.ctx, provideOutgoingTimeout)
	defer cancel()

	if err := p.contentRouting.Provide(ctx, cid, true); err != nil {
		log.Warning("Failed to provide key: %s", err)
	}
	// TODO: REMOVE
	fmt.Println("ending announce", cid)
}

// Workers

// Handle incoming requests to provide blocks
//
// Basically, buffer everything that comes through the incoming channel.
// Then, whenever the outgoing channel is ready to receive a value, pull
// a value out of the buffer and put it onto the outgoing channel.
func (p *Provider) handleIncoming() {
	var buffer []cid.Cid // unbounded buffer between incoming/outgoing
	var nextKey cid.Cid
	var keys chan cid.Cid

	for {
		select {
		case key, ok := <-p.incoming:
			// TODO: REMOVE
			fmt.Println("key from incoming", key)
			if !ok {
				log.Debug("incoming channel closed")
				return
			}

			if keys == nil {
				nextKey = key
				keys = p.outgoing
			} else {
				buffer = append(buffer, key)
			}
		case keys <- nextKey:
			// TODO: REMOVE
			fmt.Println("nextKey to keys", nextKey)
			if len(buffer) > 0 {
				nextKey = buffer[0]
				buffer = buffer[1:]
			} else {
				keys = nil
			}
		case <-p.ctx.Done():
			return
		}
	}
}

// Handle all outgoing cids by providing them
func (p *Provider) handleOutgoing() {
	limit := make(chan struct{}, provideOutgoingLimit)
	limitedProvide := func(cid cid.Cid, workerId int) {
		defer func() {
			<-limit
		}()

		ev := logging.LoggableMap{"ID": workerId}
		// TODO: EventBegin deprecated?
		defer log.EventBegin(p.ctx, "Ipfs.Provide.Worker.Work", ev, cid)

		p.Announce(cid)
	}

	for workerId := 0; ; workerId++ {
		ev := logging.LoggableMap{"ID": workerId}
		log.Event(p.ctx, "Ipfs.Provide.Worker.Loop", ev)
		select {
		case key, ok := <-p.outgoing:
			if !ok {
				log.Debug("outgoing channel closed")
				return
			}
			select {
			case limit <- struct{}{}:
				go limitedProvide(key, workerId)
			case <-p.ctx.Done():
				return
			}
		case <-p.ctx.Done():
			return
		}
	}
}
