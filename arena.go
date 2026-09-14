package main

// Arena（团战 / 群战）：N 个 Agent 围绕一个主题自由搏击。
// 关键设计：
//   - 没有"正方/反方"——所有 participant 平等
//   - 发言顺序：order_mode 决定 random / manual；manual_order 存具体序列
//   - 每条 round 可选 @ 引用上一条（reply_to_id）
//   - 末轮：每个 participant 投 1 票（不能投自己），票多 = "最具说服力"赢家
//   - 复用 task_assignments 跑单轮派发，老链路零改动

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	ArenaPending  = "pending"  // 已创建未开打
	ArenaRunning  = "running"  // 进行中
	ArenaVoting   = "voting"   // 全部 round 跑完，等投票
	ArenaFinished = "finished" // 已结算
	ArenaCanceled = "canceled"

	OrderRandom = "random"
	OrderManual = "manual"

	ArenaRoundPending   = "pending"
	ArenaRoundDelivered = "delivered"
	ArenaRoundDone      = "done"
	ArenaRoundFailed    = "failed"
)

const (
	arenaTitleMax  = 80
	arenaTopicMax  = 500
	arenaMinRounds = 3
	arenaMaxRounds = 5
	arenaMaxAgents = 7
	arenaListLimit = 200
)

type Arena struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Topic        string   `json:"topic"`
	Participants []string `json:"participants"`
	OrderMode    string   `json:"order_mode"`
	ManualOrder  []string `json:"manual_order"`
	RoundsTotal  int      `json:"rounds_total"`
	CurrentRound int      `json:"current_round"`
	Status       string   `json:"status"`
	Verdict      string   `json:"verdict"`
	Winner       string   `json:"winner"`
	CreatedAt    int64    `json:"created_at"`
	StartedAt    *int64   `json:"started_at,omitempty"`
	FinishedAt   *int64   `json:"finished_at,omitempty"`
}

type ArenaRound struct {
	ID           string `json:"id"`
	ArenaID      string `json:"arena_id"`
	Seq          int    `json:"seq"`
	SpeakerAgent string `json:"speaker_agent"`
	ReplyToID    string `json:"reply_to_id"`
	AssignmentID string `json:"assignment_id"`
	Prompt       string `json:"prompt"`
	Response     string `json:"response"`
	Status       string `json:"status"`
	CreatedAt    int64  `json:"created_at"`
	DeliveredAt  *int64 `json:"delivered_at,omitempty"`
	RespondedAt  *int64 `json:"responded_at,omitempty"`
}

type ArenaVote struct {
	ArenaID     string `json:"arena_id"`
	VoterAgent  string `json:"voter_agent"`
	TargetAgent string `json:"target_agent"`
	Reason      string `json:"reason"`
	VotedAt     int64  `json:"voted_at"`
}

var (
	errArenaNotFound = errors.New("团战不存在")
	errArenaState    = errors.New("团战状态不允许该操作")
	errArenaInvalid  = errors.New("团战参数无效")
)

// ---- store 方法 ----

func (s *store) createArena(a *Arena) error {
	if len(a.Participants) == 0 {
		a.Participants = []string{}
	}
	if len(a.ManualOrder) == 0 {
		a.ManualOrder = []string{}
	}
	pj, _ := json.Marshal(a.Participants)
	mj, _ := json.Marshal(a.ManualOrder)
	_, err := s.db.Exec(
		`INSERT INTO arenas (id, title, topic, participants, order_mode, manual_order,
			rounds_total, current_round, status, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Title, a.Topic, string(pj), a.OrderMode, string(mj),
		a.RoundsTotal, a.CurrentRound, a.Status, a.CreatedAt,
	)
	return err
}

func scanArena(row interface {
	Scan(...any) error
}, a *Arena) error {
	var pj, mj string
	if err := row.Scan(&a.ID, &a.Title, &a.Topic, &pj, &a.OrderMode, &mj,
		&a.RoundsTotal, &a.CurrentRound, &a.Status, &a.Verdict, &a.Winner,
		&a.CreatedAt, &a.StartedAt, &a.FinishedAt); err != nil {
		return err
	}
	if pj != "" {
		_ = json.Unmarshal([]byte(pj), &a.Participants)
	}
	if mj != "" {
		_ = json.Unmarshal([]byte(mj), &a.ManualOrder)
	}
	if a.Participants == nil {
		a.Participants = []string{}
	}
	if a.ManualOrder == nil {
		a.ManualOrder = []string{}
	}
	return nil
}

func (s *store) getArena(id string) (*Arena, error) {
	row := s.db.QueryRow(
		`SELECT id, title, topic, participants, order_mode, manual_order,
		        rounds_total, current_round, status, verdict, winner,
		        created_at, started_at, finished_at
		 FROM arenas WHERE id = ?`, id)
	a := &Arena{}
	if err := scanArena(row, a); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errArenaNotFound
		}
		return nil, err
	}
	return a, nil
}

func (s *store) listArenas(limit int) ([]Arena, error) {
	rows, err := s.db.Query(
		`SELECT id, title, topic, participants, order_mode, manual_order,
		        rounds_total, current_round, status, verdict, winner,
		        created_at, started_at, finished_at
		 FROM arenas ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Arena{}
	for rows.Next() {
		a := Arena{}
		if err := scanArena(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// startArena 把 arena 从 pending 切到 running，按 order_mode 确定发言顺序，
// 一次性插入所有 round 行并派发第 1 轮。
func (s *store) startArena(id string) (*Arena, []ArenaRound, error) {
	a, err := s.getArena(id)
	if err != nil {
		return nil, nil, err
	}
	if a.Status != ArenaPending {
		return nil, nil, errArenaState
	}
	if len(a.Participants) < 2 {
		return nil, nil, fmt.Errorf("%w: 至少 2 个 participant", errArenaInvalid)
	}
	if a.RoundsTotal < arenaMinRounds || a.RoundsTotal > arenaMaxRounds {
		return nil, nil, fmt.Errorf("%w: rounds_total 必须在 %d-%d 之间", errArenaInvalid, arenaMinRounds, arenaMaxRounds)
	}

	// 确定发言顺序：random 洗牌 / manual 直接用 / fallback 自动生成
	order := a.ManualOrder
	if a.OrderMode == OrderRandom || len(order) == 0 {
		order = shuffle(a.Participants)
	}
	// 校验 order 覆盖所有 participants
	if !sameSet(order, a.Participants) {
		return nil, nil, fmt.Errorf("%w: 发言顺序必须覆盖所有 participants", errArenaInvalid)
	}

	// 每轮每个 participant 一次发言：总轮次 = rounds_total，seq 编号递增
	// 顺序按 order 轮转
	plan := make([]struct{ seq int; aid string; replyTo string }, 0, a.RoundsTotal*len(order))
	for r := 0; r < a.RoundsTotal; r++ {
		for i, aid := range order {
			seq := r*len(order) + i + 1
			replyTo := ""
			if seq > 1 {
				replyTo = fmt.Sprintf("seq:%d", seq-1) // 用 "seq:N" 占位，下面解析成 id
			}
			plan = append(plan, struct {
				seq     int
				aid     string
				replyTo string
			}{seq, aid, replyTo})
		}
	}

	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`UPDATE arenas SET status = ?, started_at = ?, manual_order = ?, current_round = 0 WHERE id = ?`,
		ArenaRunning, now, arenaJSON(order), id); err != nil {
		return nil, nil, err
	}

	roundIDs := make([]string, 0, len(plan))
	for _, p := range plan {
		rid := "arnd_" + randHex(8)
		roundIDs = append(roundIDs, rid)
		if _, err := tx.Exec(
			`INSERT INTO arena_rounds (id, arena_id, seq, speaker_agent, reply_to_id, prompt, status, created_at)
			 VALUES (?,?,?,?,?,?,?,?)`,
			rid, id, p.seq, p.aid, "", "", ArenaRoundPending, now,
		); err != nil {
			return nil, nil, err
		}
	}
	// 第二轮填 reply_to_id 指向上一条 round.id
	for i, rid := range roundIDs {
		if plan[i].replyTo == "" {
			continue
		}
		// replyTo = "seq:N" → 查 roundIDs[N-1]
		var n int
		if _, err := fmt.Sscanf(plan[i].replyTo, "seq:%d", &n); err == nil && n > 0 && n <= len(roundIDs) {
			if _, err := tx.Exec(
				`UPDATE arena_rounds SET reply_to_id = ? WHERE id = ?`,
				roundIDs[n-1], rid,
			); err != nil {
				return nil, nil, err
			}
		}
	}

	// 派发第 1 轮
	if err := dispatchRoundTx(tx, a, plan[0].seq, plan[0].aid, roundIDs[0], ""); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(`UPDATE arenas SET current_round = 1 WHERE id = ?`, id); err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	a.Status = ArenaRunning
	a.StartedAt = &now
	a.CurrentRound = 1
	a.ManualOrder = order

	rounds, err := s.listArenaRounds(id)
	return a, rounds, err
}

// dispatchRoundTx 在一个事务里：建 task + task_assignment，把 assignment_id 写回 arena_round。
func dispatchRoundTx(tx *sql.Tx, a *Arena, seq int, aid, roundID, prevContext string) error {
	taskID := "tsk_" + randHex(8)
	now := time.Now().Unix()
	title := fmt.Sprintf("Arena R%d", seq)
	if _, err := tx.Exec(
		`INSERT INTO tasks (id, title, content, created_at) VALUES (?,?,?,?)`,
		taskID, title, "", now,
	); err != nil {
		return err
	}
	assignID := "tsa_" + randHex(8)
	prompt := buildArenaPrompt(a, seq, aid, prevContext)
	if _, err := tx.Exec(
		`INSERT INTO task_assignments (id, task_id, agent_id, seq, content, status, created_at)
		 VALUES (?,?,?,?,?,'pending',?)`,
		assignID, taskID, aid, seq, prompt, now,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE arena_rounds SET assignment_id = ?, prompt = ? WHERE id = ?`,
		assignID, prompt, roundID,
	); err != nil {
		return err
	}
	return nil
}

// buildArenaPrompt 拼装一轮的 prompt：主题 + 参与者列表 + 前面所有发言 + 本轮任务。
// 不带"【正方/反方/主持】"——团战里所有 agent 平等，只有"你的回合"。
func buildArenaPrompt(a *Arena, seq int, aid, prevContext string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("这是一场团战式自由讨论。主题：%s\n\n", a.Topic))
	sb.WriteString(fmt.Sprintf("【参战者（%d 人）】\n", len(a.Participants)))
	for _, p := range a.Participants {
		marker := "  "
		if p == aid {
			marker = "→ "
		}
		sb.WriteString(fmt.Sprintf("%s%s\n", marker, agentShortID(p)))
	}
	sb.WriteString("\n")
	if prevContext != "" {
		sb.WriteString("【前面所有发言】\n")
		sb.WriteString(prevContext)
		sb.WriteString("\n\n")
	}
	if seq == 1 {
		sb.WriteString("【本轮任务（第 1 轮 · 第 1 回合）】\n")
		sb.WriteString("你是这场团战的第一个发言者。请用你的视角和立场（基于你的人格 / 角色 / 知识）就主题发表你的观点。\n")
		sb.WriteString("可以赞同或反对主流意见，也可以开辟新角度。300 字以内。")
	} else {
		// 估算所在"轮 · 回合"：rounds_total 个轮，每轮 participantCount 个回合
		// arena 信息已在 prompt 头部给出（参战者 N 人），这里只给全局序号
		sb.WriteString(fmt.Sprintf("【本轮任务（发言 #%d）】\n", seq))
		sb.WriteString("请基于前面所有发言，给出你的回应。可以：\n")
		sb.WriteString("- @ 引用前面的某条观点（attack / agree / build-on）\n")
		sb.WriteString("- 引入新的论据或反例\n")
		sb.WriteString("- 总结某条意见并扩展\n300 字以内。")
	}
	return sb.String()
}

func agentShortID(id string) string {
	if len(id) < 8 {
		return id
	}
	return id[:8] + "…"
}

func (s *store) listArenaRounds(arenaID string) ([]ArenaRound, error) {
	rows, err := s.db.Query(
		`SELECT id, arena_id, seq, speaker_agent, COALESCE(reply_to_id, ''),
		        COALESCE(assignment_id, ''), prompt, response, status,
		        created_at, delivered_at, responded_at
		 FROM arena_rounds WHERE arena_id = ? ORDER BY seq ASC`, arenaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArenaRound{}
	for rows.Next() {
		var r ArenaRound
		if err := rows.Scan(&r.ID, &r.ArenaID, &r.Seq, &r.SpeakerAgent, &r.ReplyToID,
			&r.AssignmentID, &r.Prompt, &r.Response, &r.Status,
			&r.CreatedAt, &r.DeliveredAt, &r.RespondedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// advanceArena 编排器：找第一个未完成 round 派发，全部完成 → voting。
func (s *server) advanceArena(arenaID string) error {
	a, err := s.store.getArena(arenaID)
	if err != nil || a.Status != ArenaRunning {
		return err
	}
	if err := s.store.syncArenaRounds(a.ID); err != nil {
		return err
	}
	rounds, err := s.store.listArenaRounds(a.ID)
	if err != nil {
		return err
	}
	if len(rounds) == 0 {
		return nil
	}
	firstUnfinished := -1
	for i, r := range rounds {
		if r.Status != ArenaRoundDone && r.Status != ArenaRoundFailed {
			firstUnfinished = i
			break
		}
	}
	if firstUnfinished < 0 {
		if _, err := s.store.db.Exec(
			`UPDATE arenas SET status = ? WHERE id = ? AND status = ?`,
			ArenaVoting, a.ID, ArenaRunning); err != nil {
			return err
		}
		s.publishArena(a.ID, "voting")
		return nil
	}
	if firstUnfinished == 0 {
		return nil
	}

	next := rounds[firstUnfinished]
	if next.AssignmentID != "" {
		return nil
	}
	prevCtx := renderArenaContext(rounds[:firstUnfinished], a.Participants)
	tx, err := s.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := dispatchRoundTx(tx, a, next.Seq, next.SpeakerAgent, next.ID, prevCtx); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE arenas SET current_round = ? WHERE id = ?`, next.Seq, a.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.publishArena(a.ID, "next_round")
	return nil
}

func renderArenaContext(rs []ArenaRound, participants []string) string {
	var sb strings.Builder
	pmap := make(map[string]string, len(participants))
	for i, p := range participants {
		pmap[p] = fmt.Sprintf("参与者#%d", i+1)
	}
	for _, r := range rs {
		role := pmap[r.SpeakerAgent]
		if role == "" {
			role = agentShortID(r.SpeakerAgent)
		}
		sb.WriteString(fmt.Sprintf("--- 第 %d 轮 - %s ---\n", r.Seq, role))
		if r.Response != "" {
			sb.WriteString(r.Response)
		} else if r.Status == ArenaRoundFailed {
			sb.WriteString("（该轮发言失败）")
		} else {
			sb.WriteString("（等待发言）")
		}
		sb.WriteString("\n\n")
	}
	return strings.TrimSpace(sb.String())
}

// arenaIDByAssignment 反查一个 task_assignment 所属的 arena。
func (s *store) arenaIDByAssignment(assignmentID string) (string, error) {
	var id string
	err := s.db.QueryRow(
		`SELECT arena_id FROM arena_rounds WHERE assignment_id = ?`, assignmentID,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errArenaNotFound
	}
	return id, err
}

func (s *store) syncArenaRounds(arenaID string) error {
	_, err := s.db.Exec(
		`UPDATE arena_rounds
		 SET status = (SELECT ta.status FROM task_assignments ta WHERE ta.id = arena_rounds.assignment_id),
		     response = COALESCE((SELECT ta.result FROM task_assignments ta WHERE ta.id = arena_rounds.assignment_id), ''),
		     delivered_at = (SELECT ta.delivered_at FROM task_assignments ta WHERE ta.id = arena_rounds.assignment_id),
		     responded_at = (SELECT ta.result_at FROM task_assignments ta WHERE ta.id = arena_rounds.assignment_id)
		 WHERE arena_id = ? AND assignment_id IS NOT NULL`, arenaID)
	return err
}

func (s *store) castArenaVote(a *Arena, voter, target, reason string) error {
	if voter == target {
		return fmt.Errorf("%w: 不能投给自己", errArenaInvalid)
	}
	allowed := map[string]bool{}
	for _, p := range a.Participants {
		allowed[p] = true
	}
	if !allowed[voter] || !allowed[target] {
		return fmt.Errorf("%w: voter / target 必须是参战 agent", errArenaInvalid)
	}
	_, err := s.db.Exec(
		`INSERT INTO arena_votes (arena_id, voter_agent, target_agent, reason, voted_at)
		 VALUES (?,?,?,?,?)
		 ON CONFLICT(arena_id, voter_agent) DO UPDATE SET target_agent = excluded.target_agent, reason = excluded.reason, voted_at = excluded.voted_at`,
		a.ID, voter, target, reason, time.Now().Unix(),
	)
	return err
}

func (s *store) listArenaVotes(arenaID string) ([]ArenaVote, error) {
	rows, err := s.db.Query(
		`SELECT arena_id, voter_agent, target_agent, reason, voted_at
		 FROM arena_votes WHERE arena_id = ? ORDER BY voted_at ASC`, arenaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArenaVote{}
	for rows.Next() {
		var v ArenaVote
		if err := rows.Scan(&v.ArenaID, &v.VoterAgent, &v.TargetAgent, &v.Reason, &v.VotedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// finalizeArena 投票数达标后结算：winner = 票数最多的 agent。
func (s *store) finalizeArena(arenaID string) error {
	a, err := s.getArena(arenaID)
	if err != nil {
		return err
	}
	votes, err := s.listArenaVotes(arenaID)
	if err != nil {
		return err
	}
	tally := map[string]int{}
	for _, v := range votes {
		tally[v.TargetAgent]++
	}
	winner := ""
	bestN := -1
	for _, p := range a.Participants {
		n := tally[p]
		if n > bestN {
			bestN = n
			winner = p
		}
	}
	now := time.Now().Unix()
	verdict := buildFinalVerdict(a, tally, votes, winner)
	_, err = s.db.Exec(
		`UPDATE arenas SET status = ?, verdict = ?, winner = ?, finished_at = ? WHERE id = ?`,
		ArenaFinished, verdict, winner, now, arenaID,
	)
	return err
}

func buildFinalVerdict(a *Arena, tally map[string]int, votes []ArenaVote, winner string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("团战「%s」结束。\n\n", a.Title))
	sb.WriteString("【票数统计】\n")
	for _, p := range a.Participants {
		n := tally[p]
		marker := "  "
		if p == winner {
			marker = "🏆"
		}
		sb.WriteString(fmt.Sprintf("%s %s：%d 票\n", marker, agentShortID(p), n))
	}
	sb.WriteString("\n【投票理由】\n")
	for _, v := range votes {
		if v.Reason == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("  %s → %s：%s\n", agentShortID(v.VoterAgent), agentShortID(v.TargetAgent), v.Reason))
	}
	if winner != "" {
		sb.WriteString(fmt.Sprintf("\n最具说服力：%s", agentShortID(winner)))
	}
	return sb.String()
}

// ---- HTTP 路由 ----

func (s *server) handleCreateArena(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title        string   `json:"title"`
		Topic        string   `json:"topic"`
		Participants []string `json:"participants"`
		OrderMode    string   `json:"order_mode"`
		ManualOrder  []string `json:"manual_order"`
		RoundsTotal  int      `json:"rounds_total"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Topic = strings.TrimSpace(req.Topic)
	if req.Title == "" || len(req.Title) > arenaTitleMax {
		writeError(w, http.StatusBadRequest, "title 不能为空且不超过 80 字")
		return
	}
	if req.Topic == "" || len(req.Topic) > arenaTopicMax {
		writeError(w, http.StatusBadRequest, "topic 不能为空且不超过 500 字")
		return
	}
	if len(req.Participants) < 2 || len(req.Participants) > arenaMaxAgents {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("participants 必须在 2-%d 之间", arenaMaxAgents))
		return
	}
	for _, aid := range req.Participants {
		if _, err := s.store.agentByID(aid); err != nil {
			writeError(w, http.StatusBadRequest, "participants 包含未注册 agent: "+aid)
			return
		}
	}
	// 去重
	seen := map[string]bool{}
	uniq := []string{}
	for _, p := range req.Participants {
		if !seen[p] {
			seen[p] = true
			uniq = append(uniq, p)
		}
	}
	if len(uniq) != len(req.Participants) {
		writeError(w, http.StatusBadRequest, "participants 含重复 agent")
		return
	}
	if req.RoundsTotal < arenaMinRounds || req.RoundsTotal > arenaMaxRounds {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("rounds_total 必须在 %d-%d 之间", arenaMinRounds, arenaMaxRounds))
		return
	}
	if req.OrderMode == "" {
		req.OrderMode = OrderRandom
	}
	if req.OrderMode != OrderRandom && req.OrderMode != OrderManual {
		writeError(w, http.StatusBadRequest, "order_mode 必须是 random 或 manual")
		return
	}
	if req.OrderMode == OrderManual {
		if len(req.ManualOrder) != len(uniq) {
			writeError(w, http.StatusBadRequest, "manual 模式必须指定完整发言顺序")
			return
		}
		if !sameSet(req.ManualOrder, uniq) {
			writeError(w, http.StatusBadRequest, "manual_order 必须覆盖所有 participants")
			return
		}
	} else {
		req.ManualOrder = nil
	}
	a := &Arena{
		ID:           "arn_" + randHex(8),
		Title:        req.Title,
		Topic:        req.Topic,
		Participants: uniq,
		OrderMode:    req.OrderMode,
		ManualOrder:  req.ManualOrder,
		RoundsTotal:  req.RoundsTotal,
		CurrentRound: 0,
		Status:       ArenaPending,
		CreatedAt:    time.Now().Unix(),
	}
	if err := s.store.createArena(a); err != nil {
		log.Printf("创建团战失败: %v", err)
		writeError(w, http.StatusInternalServerError, "内部错误")
		return
	}
	s.publishArena(a.ID, "created")
	writeJSON(w, http.StatusOK, a)
}

func (s *server) handleListArenas(w http.ResponseWriter, r *http.Request) {
	as, err := s.store.listArenas(arenaListLimit)
	if err != nil {
		log.Printf("列团战失败: %v", err)
		writeError(w, http.StatusInternalServerError, "内部错误")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"arenas": as})
}

func (s *server) handleGetArena(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, err := s.store.getArena(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "团战不存在")
		return
	}
	if a.Status == ArenaRunning {
		_ = s.advanceArena(a.ID)
		a, _ = s.store.getArena(a.ID)
	}
	rounds, err := s.store.listArenaRounds(a.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "内部错误")
		return
	}
	_ = s.store.syncArenaRounds(a.ID)
	rounds, _ = s.store.listArenaRounds(a.ID)
	votes, _ := s.store.listArenaVotes(a.ID)
	agents, _ := s.store.agentsByIDs(a.Participants)
	tally := map[string]int{}
	for _, v := range votes {
		tally[v.TargetAgent]++
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"arena":  a,
		"rounds": rounds,
		"votes":  votes,
		"agents": agents,
		"tally":  tally,
	})
}

func (s *server) handleStartArena(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, rounds, err := s.store.startArena(id)
	if err != nil {
		switch {
		case errors.Is(err, errArenaNotFound):
			writeError(w, http.StatusNotFound, "团战不存在")
		case errors.Is(err, errArenaState):
			writeError(w, http.StatusConflict, "团战已开始或已结束")
		case errors.Is(err, errArenaInvalid):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			log.Printf("开打失败: %v", err)
			writeError(w, http.StatusInternalServerError, "内部错误")
		}
		return
	}
	s.publish("tasks")
	s.publishArena(a.ID, "started")
	writeJSON(w, http.StatusOK, map[string]any{"arena": a, "rounds": rounds})
}

func (s *server) handleCastVote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		VoterAgent  string `json:"voter_agent"`
		TargetAgent string `json:"target_agent"`
		Reason      string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if len(req.Reason) > 500 {
		writeError(w, http.StatusBadRequest, "reason 不能超过 500 字")
		return
	}
	a, err := s.store.getArena(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "团战不存在")
		return
	}
	if a.Status != ArenaVoting && a.Status != ArenaFinished {
		writeError(w, http.StatusConflict, "团战尚未进入投票阶段")
		return
	}
	if err := s.store.castArenaVote(a, req.VoterAgent, req.TargetAgent, req.Reason); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	votes, _ := s.store.listArenaVotes(a.ID)
	if len(votes) >= len(a.Participants) && a.Status == ArenaVoting {
		if err := s.store.finalizeArena(a.ID); err != nil {
			log.Printf("结算团战失败: %v", err)
		}
		s.publishArena(a.ID, "finished")
	}
	s.publishArena(a.ID, "vote")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleCancelArena(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, err := s.store.getArena(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "团战不存在")
		return
	}
	if a.Status == ArenaFinished || a.Status == ArenaCanceled {
		writeError(w, http.StatusConflict, "团战已结束")
		return
	}
	if _, err := s.store.db.Exec(
		`UPDATE arenas SET status = ?, finished_at = ? WHERE id = ?`,
		ArenaCanceled, time.Now().Unix(), id,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "内部错误")
		return
	}
	s.publishArena(id, "canceled")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) publishArena(arenaID, kind string) {
	if s.broker == nil {
		return
	}
	s.broker.publish("arena:" + kind + ":" + arenaID)
	s.broker.publish("arenas")
}

// ---- 工具 ----

func shuffle(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	for i := len(out) - 1; i > 0; i-- {
		var b [8]byte
		_, _ = rand.Read(b[:])
		j := int(b[0]) % (i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, y := range b {
		if !m[y] {
			return false
		}
	}
	return true
}

// arena.go 自己的 JSON 辅助（避免和 tasks_test.go 里的同名 mustJSON 冲突）
func arenaJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
