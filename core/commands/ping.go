package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ipfs/go-ipfs/core/commands/cmdenv"

	pstore "gx/ipfs/QmQAGG1zxfePqj2t7bLxyN8AFccZ889DDR9Gn8kVLDrGZo/go-libp2p-peerstore"
	ma "gx/ipfs/QmRKLtwMw131aK7ugC3G7ybpumMz78YrJe5dzneyindvG1/go-multiaddr"
	cmds "gx/ipfs/QmT7zdrgdq7LD5YwWzEwnFdMf2B9Jpbbx6A6zERWEDxLGA/go-ipfs-cmds"
	iaddr "gx/ipfs/QmUSE3APe1pMFVsUBZUZaKQKERiPteCWvTAERtVQmtXzgE/go-ipfs-addr"
	ping "gx/ipfs/QmVvV8JQmmqPCwXAaesWJPheUiEFQJ9HWRhWhuFuxVQxpR/go-libp2p/p2p/protocol/ping"
	"gx/ipfs/QmcqU6QUDSXprb1518vYDGczrTJTyGwLG9eUa5iNX4xUtS/go-libp2p-peer"
	cmdkit "gx/ipfs/Qmde5VP1qUkyQXKCfmEUA7bP64V2HAptbJ7phuPp7jXWwg/go-ipfs-cmdkit"
)

const kPingTimeout = 10 * time.Second

type PingResult struct {
	Success bool
	Time    time.Duration
	Text    string
}

const (
	pingCountOptionName = "count"
)

// ErrPingSelf is returned when the user attempts to ping themself.
var ErrPingSelf = errors.New("error: can't ping self")

var PingCmd = &cmds.Command{
	Helptext: cmdkit.HelpText{
		Tagline: "Send echo request packets to IPFS hosts.",
		ShortDescription: `
'ipfs ping' is a tool to test sending data to other nodes. It finds nodes
via the routing system, sends pings, waits for pongs, and prints out round-
trip latency information.
		`,
	},
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("peer ID", true, true, "ID of peer to be pinged.").EnableStdin(),
	},
	Options: []cmdkit.Option{
		cmdkit.IntOption(pingCountOptionName, "n", "Number of ping messages to send.").WithDefault(10),
	},
	Run: func(req *cmds.Request, res cmds.ResponseEmitter, env cmds.Environment) error {
		n, err := cmdenv.GetNode(env)
		if err != nil {
			return err
		}

		// Must be online!
		if !n.OnlineMode() {
			return ErrNotOnline
		}

		addr, pid, err := ParsePeerParam(req.Arguments[0])
		if err != nil {
			return fmt.Errorf("failed to parse peer address '%s': %s", req.Arguments[0], err)
		}

		if pid == n.Identity {
			return ErrPingSelf
		}

		if addr != nil {
			n.Peerstore.AddAddr(pid, addr, pstore.TempAddrTTL) // temporary
		}

		numPings, _ := req.Options[pingCountOptionName].(int)
		if numPings <= 0 {
			return fmt.Errorf("error: ping count must be greater than 0, was %d", numPings)
		}

		if len(n.Peerstore.Addrs(pid)) == 0 {
			// Make sure we can find the node in question
			if err := res.Emit(&PingResult{
				Text:    fmt.Sprintf("Looking up peer %s", pid.Pretty()),
				Success: true,
			}); err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(req.Context, kPingTimeout)
			p, err := n.Routing.FindPeer(ctx, pid)
			cancel()
			if err != nil {
				return res.Emit(&PingResult{Text: fmt.Sprintf("Peer lookup error: %s", err)})
			}
			n.Peerstore.AddAddrs(p.ID, p.Addrs, pstore.TempAddrTTL)
		}

		if err := res.Emit(&PingResult{
			Text:    fmt.Sprintf("PING %s.", pid.Pretty()),
			Success: true,
		}); err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(req.Context, kPingTimeout*time.Duration(numPings))
		defer cancel()
		pings, err := ping.Ping(ctx, n.PeerHost, pid)
		if err != nil {
			return res.Emit(&PingResult{
				Success: false,
				Text:    fmt.Sprintf("Ping error: %s", err),
			})
		}

		var total time.Duration
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for i := 0; i < numPings; i++ {
			t, ok := <-pings
			if !ok {
				break
			}

			if err := res.Emit(&PingResult{
				Success: true,
				Time:    t,
			}); err != nil {
				return err
			}

			total += t

			select {
			case <-ticker.C:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		averagems := total.Seconds() * 1000 / float64(numPings)
		return res.Emit(&PingResult{
			Success: true,
			Text:    fmt.Sprintf("Average latency: %.2fms", averagems),
		})
	},
	Type: PingResult{},
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeTypedEncoder(func(req *cmds.Request, w io.Writer, out *PingResult) error {
			if len(out.Text) > 0 {
				fmt.Fprintln(w, out.Text)
			} else if out.Success {
				fmt.Fprintf(w, "Pong received: time=%.2f ms\n", out.Time.Seconds()*1000)
			} else {
				fmt.Fprintf(w, "Pong failed\n")
			}
			return nil
		}),
	},
}

func ParsePeerParam(text string) (ma.Multiaddr, peer.ID, error) {
	// Multiaddr
	if strings.HasPrefix(text, "/") {
		a, err := iaddr.ParseString(text)
		if err != nil {
			return nil, "", err
		}
		return a.Transport(), a.ID(), nil
	}
	// Raw peer ID
	p, err := peer.IDB58Decode(text)
	return nil, p, err
}
