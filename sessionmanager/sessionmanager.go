package sessionmanager

import (
	"context"
	"sync"
	"time"

	cid "github.com/ipfs/go-cid"
	delay "github.com/ipfs/go-ipfs-delay"

	bsbpm "github.com/ipfs/go-bitswap/blockpresencemanager"
	notifications "github.com/ipfs/go-bitswap/notifications"
	bssession "github.com/ipfs/go-bitswap/session"
	bssim "github.com/ipfs/go-bitswap/sessioninterestmanager"
	exchange "github.com/ipfs/go-ipfs-exchange-interface"
	logging "github.com/ipfs/go-log"
	peer "github.com/libp2p/go-libp2p-core/peer"
)

var log = logging.Logger("bitswap")

// Session is a session that is managed by the session manager
type Session interface {
	exchange.Fetcher
	ID() uint64
	ReceiveFrom(peer.ID, []cid.Cid, []cid.Cid, []cid.Cid)
}

// SessionFactory generates a new session for the SessionManager to track.
type SessionFactory func(ctx context.Context, id uint64, sprm bssession.SessionPeerManager, sim *bssim.SessionInterestManager, pm bssession.PeerManager, bpm *bsbpm.BlockPresenceManager, notif notifications.PubSub, provSearchDelay time.Duration, rebroadcastDelay delay.D, self peer.ID) Session

// PeerManagerFactory generates a new peer manager for a session.
type PeerManagerFactory func(ctx context.Context, id uint64) bssession.SessionPeerManager

// SessionManager is responsible for creating, managing, and dispatching to
// sessions.
type SessionManager struct {
	ctx                    context.Context
	sessionFactory         SessionFactory
	sessionInterestManager *bssim.SessionInterestManager
	peerManagerFactory     PeerManagerFactory
	blockPresenceManager   *bsbpm.BlockPresenceManager
	peerManager            bssession.PeerManager
	notif                  notifications.PubSub

	// Sessions
	sessLk   sync.RWMutex
	sessions map[uint64]Session

	// Session Index
	sessIDLk sync.Mutex
	sessID   uint64

	self peer.ID
}

// New creates a new SessionManager.
func New(ctx context.Context, sessionFactory SessionFactory, sessionInterestManager *bssim.SessionInterestManager, peerManagerFactory PeerManagerFactory,
	blockPresenceManager *bsbpm.BlockPresenceManager, peerManager bssession.PeerManager, notif notifications.PubSub, self peer.ID) *SessionManager {
	return &SessionManager{
		ctx:                    ctx,
		sessionFactory:         sessionFactory,
		sessionInterestManager: sessionInterestManager,
		peerManagerFactory:     peerManagerFactory,
		blockPresenceManager:   blockPresenceManager,
		peerManager:            peerManager,
		notif:                  notif,
		sessions:               make(map[uint64]Session),
		self:                   self,
	}
}

// NewSession initializes a session with the given context, and adds to the
// session manager.
func (sm *SessionManager) NewSession(ctx context.Context,
	provSearchDelay time.Duration,
	rebroadcastDelay delay.D) exchange.Fetcher {
	id := sm.GetNextSessionID()
	sessionctx, cancel := context.WithCancel(ctx)

	pm := sm.peerManagerFactory(sessionctx, id)
	session := sm.sessionFactory(sessionctx, id, pm, sm.sessionInterestManager, sm.peerManager, sm.blockPresenceManager, sm.notif, provSearchDelay, rebroadcastDelay, sm.self)
	sm.sessLk.Lock()
	sm.sessions[id] = session
	sm.sessLk.Unlock()
	go func() {
		defer cancel()
		select {
		case <-sm.ctx.Done():
			sm.removeSession(id)
		case <-ctx.Done():
			sm.removeSession(id)
		}
	}()

	return session
}

func (sm *SessionManager) removeSession(sesid uint64) {
	sm.sessLk.Lock()
	defer sm.sessLk.Unlock()

	/* FIXME
	for i := 0; i < len(sm.sessions); i++ {
		if sm.sessions[i] == session {
			switch sess := session.session.(type) {
			case *bssession.Session:
				log.Event(context.TODO(), "jimbssessdone", logging.Metadata{
					"sessionUuid": sess.UUID(),
				})
			}
			sm.sessions[i] = sm.sessions[len(sm.sessions)-1]
			sm.sessions[len(sm.sessions)-1] = sesTrk{} // free memory.
			sm.sessions = sm.sessions[:len(sm.sessions)-1]
			return
		}
	}
	*/

	delete(sm.sessions, sesid)
}

// GetNextSessionID returns the next sequential identifier for a session.
func (sm *SessionManager) GetNextSessionID() uint64 {
	sm.sessIDLk.Lock()
	defer sm.sessIDLk.Unlock()

	sm.sessID++
	return sm.sessID
}

func (sm *SessionManager) ReceiveFrom(p peer.ID, blks []cid.Cid, haves []cid.Cid, dontHaves []cid.Cid) []Session {
	sessions := make([]Session, 0)

	// Notify each session that is interested in the blocks / HAVEs / DONT_HAVEs
	for _, id := range sm.sessionInterestManager.InterestedSessions(blks, haves, dontHaves) {
		sm.sessLk.RLock()
		sess, ok := sm.sessions[id]
		sm.sessLk.RUnlock()

		if ok {
			sess.ReceiveFrom(p, blks, haves, dontHaves)
			sessions = append(sessions, sess)
		}
	}

	return sessions
}
