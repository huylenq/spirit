package daemon

import (
	"bufio"
	"encoding/json"
	"net"
)

func (d *Daemon) acceptLoop() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			return // listener closed
		}
		d.clientConnected()
		go d.handleConn(conn)
	}
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()
	defer d.clientDisconnected()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	enc := json.NewEncoder(conn)

	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			enc.Encode(Response{Type: RespError, Error: "invalid JSON: " + err.Error()})
			continue
		}

		resp := d.dispatch(req, conn, enc)
		if resp == nil {
			// subscribe handler manages its own writes
			return
		}
		enc.Encode(resp)
	}
}

func (d *Daemon) dispatch(req Request, conn net.Conn, enc *json.Encoder) *Response {
	switch req.Type {
	case ReqPing:
		r := Response{Type: RespPong}
		return &r

	case ReqNudge:
		return d.handleNudge(req.Data)

	case ReqSubscribe:
		d.handleSubscribe(conn, enc)
		return nil // subscribe manages its own lifecycle

	case ReqTranscript:
		return d.handleTranscript(req.Data)

	case ReqDiffStats:
		return d.handleDiffStats(req.Data)

	case ReqSummary:
		return d.handleSummary(req.Data)

	case ReqSynthesize:
		return d.handleSynthesize(req.Data)

	case ReqSynthesizeAll:
		return d.handleSynthesizeAll(req.Data)

	case ReqHookEvents:
		return d.handleHookEvents(req.Data)

	case ReqAllHookEffects:
		return d.handleAllHookEffects()

	case ReqRawTranscript:
		return d.handleRawTranscript(req.Data)

	case ReqPaneGeometry:
		return d.handlePaneGeometry(req.Data)

	case ReqLater:
		return d.handleLater(req.Data)

	case ReqLaterKill:
		return d.handleLaterKill(req.Data)

	case ReqUnlater:
		return d.handleUnlater(req.Data)

	case ReqOpenLater:
		return d.handleOpenLater(req.Data)

	case ReqRenameWindow:
		return d.handleRenameWindow(req.Data)

	case ReqCommitOnly:
		return d.handleCommit(req.Data, false)

	case ReqCommitDone:
		return d.handleCommit(req.Data, true)

	case ReqCancelCommitDone:
		return d.handleCancelCommitDone(req.Data)

	case ReqQueue:
		return d.handleQueue(req.Data)

	case ReqCancelQueueItem:
		return d.handleCancelQueueItem(req.Data)

	case ReqDiffHunks:
		return d.handleDiffHunks(req.Data)

	case ReqPendingPrompt:
		return d.handlePendingPrompt(req.Data)

	case ReqRegisterOrchestrator:
		return d.handleRegisterOrchestrator(req.Data)

	case ReqUnregisterOrchestrator:
		return d.handleUnregisterOrchestrator(req.Data)

	case ReqSessions:
		return d.handleSessions(req.Data)

	case ReqSend:
		return d.handleSend(req.Data)

	case ReqSpawn:
		return d.handleSpawn(req.Data)

	case ReqKill:
		return d.handleKill(req.Data)

	case ReqDigest:
		return d.handleDigest()

	case ReqApplyTitle:
		return d.handleApplyTitle(req.Data)

	case ReqSetTags:
		return d.handleSetTags(req.Data)

	case ReqBacklogList:
		return d.handleBacklogList(req.Data)

	case ReqBacklogCreate:
		return d.handleBacklogCreate(req.Data)

	case ReqBacklogUpdate:
		return d.handleBacklogUpdate(req.Data)

	case ReqBacklogDelete:
		return d.handleBacklogDelete(req.Data)

	case ReqCopilotChat:
		return d.handleCopilotChat(req.Data)

	case ReqCopilotCancel:
		return d.handleCopilotCancel()

	case ReqCopilotStatus:
		return d.handleCopilotStatus()

	case ReqCopilotHistory:
		return d.handleCopilotHistory()

	case ReqCopilotClearHistory:
		return d.handleCopilotClearHistory()

	case ReqCopilotTogglePreamble:
		return d.handleCopilotTogglePreamble()

	default:
		r := Response{Type: RespError, Error: "unknown request type: " + req.Type}
		return &r
	}
}
