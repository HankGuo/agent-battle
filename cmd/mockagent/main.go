// Command mockagent 是 agent-matrix 团战平台的"假 Agent"实现。
//
// 启动后行为：
//  1. 调 POST /api/register 完成注册（需提供一次性 ame_ 令牌）
//  2. 循环发心跳、拉任务
//  3. 拉到的任务根据 prompt 内容生成 fake 回应
//  4. POST .../result 回写结果
//
// 用法：
//   mockagent --url=http://localhost:26817 --enroll=ame_xxx --name=pro-rocker
//
// 适用场景：极客自嗨型 demo 跑得起来，不用装真 LLM Agent。
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const heartbeatInterval = 5 * time.Second

type registerReq struct {
	Token    string `json:"token"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Meta     string `json:"meta"`
}

type registerResp struct {
	AgentID        string `json:"agent_id"`
	HeartbeatToken string `json:"heartbeat_token"`
	PollInterval   int    `json:"heartbeat_interval"`
	Error          string `json:"error"`
}

type pulledTask struct {
	AssignmentID string `json:"assignment_id"`
	TaskID       string `json:"task_id"`
	Title        string `json:"title"`
	Content      string `json:"content"`
	CreatedAt    int64  `json:"created_at"`
}

type pullResp struct {
	Tasks      []pulledTask `json:"tasks"`
	ServerTime int64        `json:"server_time"`
}

type writeResultReq struct {
	Status string `json:"status"`
	Result string `json:"result"`
}

func main() {
	url := flag.String("url", "http://localhost:26817", "server base URL")
	enroll := flag.String("enroll", os.Getenv("AM_ENROLL_TOKEN"), "一次性注册令牌 ame_…")
	name := flag.String("name", "mock-"+fmt.Sprint(time.Now().Unix()%1000), "登记名称")
	persona := flag.String("persona", "", "人设，写入 meta.persona（影响回应风格）")
	model := flag.String("model", "mock-v1", "模型标识，写入 meta.model")
	once := flag.Bool("once", false, "只跑一轮：注册 + 拉一次任务 + 回写后退出")
	flag.Parse()

	if *enroll == "" {
		log.Fatal("缺少注册令牌：用 --enroll=ame_xxx 或 AM_ENROLL_TOKEN=ame_xxx")
	}
	if !strings.HasPrefix(*enroll, "ame_") {
		log.Fatal("注册令牌必须以 ame_ 开头")
	}

	meta := buildMeta(*persona, *model)
	tok, agentID, err := register(*url, *enroll, *name, meta)
	if err != nil {
		log.Fatalf("注册失败: %v", err)
	}
	log.Printf("✅ 注册成功：%s (id=%s)", *name, agentID)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Printf("🛑 收到退出信号，bye")
		os.Exit(0)
	}()

	for {
		if err := heartbeat(*url, tok); err != nil {
			log.Printf("心跳失败: %v", err)
		}
		tasks, err := pullTasks(*url, tok)
		if err != nil {
			log.Printf("拉任务失败: %v", err)
		} else {
			for _, t := range tasks {
				log.Printf("📥 拉取到任务: %s / assignment=%s", t.Title, t.AssignmentID)
				if err := writeResult(*url, tok, t.AssignmentID, t.Content, *persona); err != nil {
					log.Printf("回写失败 (assignment=%s): %v", t.AssignmentID, err)
				}
			}
		}
		if *once {
			break
		}
		time.Sleep(heartbeatInterval)
	}
}

func buildMeta(persona, model string) string {
	m := map[string]string{"executor": "mockagent", "executor_version": "0.2.0"}
	if persona != "" {
		m["persona"] = persona
	}
	if model != "" {
		m["model"] = model
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func register(base, token, name, meta string) (string, string, error) {
	body, _ := json.Marshal(registerReq{
		Token:    token,
		Name:     name,
		Hostname: safeHostname(),
		OS:       "darwin",
		Arch:     "arm64",
		Meta:     meta,
	})
	resp, err := http.Post(strings.TrimRight(base, "/")+"/api/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var rr registerResp
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusCreated {
		return "", "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, rr.Error)
	}
	return rr.HeartbeatToken, rr.AgentID, nil
}

func safeHostname() string {
	h, _ := os.Hostname()
	if h == "" {
		return "mock-host"
	}
	return h
}

func heartbeat(base, tok string) error {
	req, _ := http.NewRequest("POST", strings.TrimRight(base, "/")+"/api/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func pullTasks(base, tok string) ([]pulledTask, error) {
	req, _ := http.NewRequest("GET", strings.TrimRight(base, "/")+"/api/agent/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var pr pullResp
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	return pr.Tasks, nil
}

func writeResult(base, tok, assignmentID, prompt, persona string) error {
	resp := composeArenaResponse(prompt, persona)
	body, _ := json.Marshal(writeResultReq{Status: "done", Result: resp})
	url := fmt.Sprintf("%s/api/agent/tasks/%s/result", strings.TrimRight(base, "/"), assignmentID)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(r.Body)
		return fmt.Errorf("HTTP %d: %s", r.StatusCode, string(b))
	}
	log.Printf("  ↳ 回写完成（%d 字）", len([]rune(resp)))
	return nil
}

// composeArenaResponse 根据 prompt 里的"团战"标志 + 轮次生成 fake 回应。
// 团战没有"正方/反方"——所有 agent 平等；mockagent 用 seq 决定发言风格。
func composeArenaResponse(prompt, persona string) string {
	if !strings.Contains(prompt, "团战式自由讨论") {
		return "已收到。"
	}
	seq := extractSeq(prompt)
	topic := extractTopic(prompt)
	hasPrior := strings.Contains(prompt, "前面所有发言")
	mention := ""
	if hasPrior {
		mention = "@" + pickRecentSpeaker(prompt)
	}
	p := strings.TrimSpace(persona)
	if p == "" {
		p = "客观中立"
	}
	switch {
	case seq == 1:
		// 开场立论
		return fmt.Sprintf(
			"从「%s」切入，我先亮明立场：%s。\n\n核心论据有三点。\n第一，事实基础充分——这一命题在长期实践中已被反复验证。\n第二，可操作性明确——路径清晰、边界清楚。\n第三，整体收益大于成本——即便有局部损失，权衡后仍值得推进。\n希望后续发言能基于具体论据展开，而不是停留在口号层面。",
			truncate(topic, 30), p,
		)
	case seq%3 == 0:
		// 每 3 个发言变一次风格：开 / 反 / 补
		return fmt.Sprintf(
			"%s 我大体同意上一轮的方向，但需要补两点。\n\n第一，数据维度——只看单一指标会失真，建议引入对照组。\n第二，时序维度——短期波动不等于长期趋势。\n\n另外想接一个被忽视的角度：边缘案例的处理方式决定整体可推广性。",
			mention,
		)
	case seq%2 == 0:
		// 偶数序号：反驳
		return fmt.Sprintf(
			"%s 上一轮的论证有点理想化。\n\n具体反驳：\n  1. 你依赖的假设缺乏可重复的实证支撑。\n  2. 你提出的替代方案恰好暴露了原命题的内在矛盾。\n  3. 类似尝试在多个领域已经失败，前车之鉴不容忽视。\n\n请正面回应：你论据的边界条件是什么？",
			mention,
		)
	default:
		// 奇数序号：补充 / 同意
		return fmt.Sprintf(
			"%s 接上一轮，我顺着说一点。\n\n我的视角：%s。\n我认为这不矛盾——前者是路径问题，后者是边界问题。\n具体来说，可以分两步走：先小范围试点，再决定是否推广。",
			mention, p,
		)
	}
}

var seqRe = regexp.MustCompile(`发言 #(\d+)`)

func extractSeq(prompt string) int {
	m := seqRe.FindStringSubmatch(prompt)
	if len(m) < 2 {
		return 1
	}
	var n int
	fmt.Sscanf(m[1], "%d", &n)
	if n < 1 {
		return 1
	}
	return n
}

func extractTopic(prompt string) string {
	const marker = "主题："
	i := strings.Index(prompt, marker)
	if i < 0 {
		return "当前主题"
	}
	rest := prompt[i+len(marker):]
	if j := strings.IndexAny(rest, "\n\r"); j > 0 {
		return strings.TrimSpace(rest[:j])
	}
	return strings.TrimSpace(rest)
}

func pickRecentSpeaker(prompt string) string {
	// 从"前面所有发言"段挑最后一个"参与者#N"作为引用目标
	const section = "前面所有发言"
	idx := strings.Index(prompt, section)
	if idx < 0 {
		return "前一位"
	}
	tail := prompt[idx:]
	re := regexp.MustCompile(`参与者#(\d+)`)
	matches := re.FindAllString(tail, -1)
	if len(matches) == 0 {
		return "前一位"
	}
	return matches[len(matches)-1]
}

func truncate(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}
