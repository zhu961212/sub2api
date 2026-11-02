package service

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	ModelEvaluationResultsExtraKey = "model_evaluation_results"
	modelEvaluationMaxAnswer       = 256 << 10
	ModelEvaluationHistoryLimit    = 5
	modelEvaluationHistoryLimit    = ModelEvaluationHistoryLimit
	modelEvaluationHistoryMaxBytes = 1 << 20
	modelEvaluationTimeout         = 10 * time.Minute
)

// The candy challenge and grading rule follow haowang02/cpa-plugin-codex-candy-eval.
// See licenses/cpa-plugin-codex-candy-eval.LICENSE for its MIT license.
const modelEvaluationCandyPrompt = `不使用任何外部工具回答以下问题：

在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）

        苹果味  桃子味  西瓜味
圆形       7      9      8
五角星形   7      6      4
`

// Preserve the reference's bicycle animation challenge, requesting a standalone
// SVG instead of executable HTML so the admin page can use an image preview.
const modelEvaluationPelicanPrompt = `不使用任何工具，也不要创建或修改文件，直接在回复中完成以下任务：

使用 SVG 绘制一个鹈鹕骑自行车的 2D 动画，你不需要任何测试，不要使用外部资产。

在一个` + " ```svg " + `代码块中给出完整、独立的 SVG 文件。动画使用 SVG 自身的动画元素或 SVG 内部 CSS，不使用 JavaScript、外部链接或 foreignObject。`

type ModelEvaluationRequest struct {
	Kind    string `json:"kind"`
	ModelID string `json:"model_id"`
	Effort  string `json:"effort"`
	Runs    int    `json:"runs"`
}

type ModelEvaluationResult struct {
	ID                     string  `json:"id"`
	Kind                   string  `json:"kind"`
	Model                  string  `json:"model"`
	Effort                 string  `json:"effort"`
	Status                 string  `json:"status"`
	Run                    int     `json:"run"`
	Runs                   int     `json:"runs"`
	Answer                 string  `json:"answer"`
	SVG                    string  `json:"svg,omitempty"`
	Error                  string  `json:"error,omitempty"`
	DurationMS             int64   `json:"duration_ms"`
	TestedAt               string  `json:"tested_at"`
	InputTokens            int64   `json:"input_tokens"`
	OutputTokens           int64   `json:"output_tokens"`
	ReasoningTokens        int64   `json:"reasoning_tokens"`
	Source                 string  `json:"source"`
	SchedulingStopped      bool    `json:"scheduling_stopped"`
	SchedulingStoppedAt    string  `json:"scheduling_stopped_at,omitempty"`
	SchedulingStopReason   string  `json:"scheduling_stop_reason,omitempty"`
	SchedulingResumed      bool    `json:"scheduling_resumed"`
	SchedulingResumedAt    string  `json:"scheduling_resumed_at,omitempty"`
	SchedulingResumeReason string  `json:"scheduling_resume_reason,omitempty"`
	ProxyID                **int64 `json:"proxy_id,omitempty"`
	ProxyName              string  `json:"proxy_name,omitempty"`
}

func normalizeModelEvaluationRequest(req ModelEvaluationRequest) (ModelEvaluationRequest, error) {
	req.Kind = strings.TrimSpace(req.Kind)
	if req.Kind == "" {
		req.Kind = "candy"
	}
	if req.Kind != "candy" && req.Kind != "pelican" {
		return req, infraerrors.BadRequest("INVALID_EVALUATION_KIND", "Choose candy or pelican evaluation")
	}
	req.ModelID = strings.TrimSpace(req.ModelID)
	if req.ModelID == "" {
		req.ModelID = openai.DefaultTestModel
	}
	if len(req.ModelID) > 200 || strings.IndexFunc(req.ModelID, unicode.IsControl) >= 0 {
		return req, infraerrors.BadRequest("INVALID_EVALUATION_MODEL", "Invalid evaluation model")
	}
	req.Effort = strings.TrimSpace(req.Effort)
	if req.Effort == "" {
		req.Effort = "low"
	}
	switch req.Effort {
	case "none", "minimal", "low", "medium", "high", "xhigh":
	default:
		return req, infraerrors.BadRequest("INVALID_REASONING_EFFORT", "Invalid reasoning effort")
	}
	if req.Runs == 0 {
		req.Runs = 1
	}
	if req.Runs < 1 || req.Runs > 10 {
		return req, infraerrors.BadRequest("INVALID_EVALUATION_RUNS", "Runs must be between 1 and 10")
	}
	return req, nil
}

// EvaluateOpenAIAccount streams diagnostics on one pinned account. Completed
// candy verdicts update scheduling with history; errors and drawings do not.
func (s *AccountTestService) EvaluateOpenAIAccount(c *gin.Context, accountID int64, request ModelEvaluationRequest) error {
	req, err := normalizeModelEvaluationRequest(request)
	if err != nil {
		return err
	}
	if s.accountRepo == nil || s.httpUpstream == nil {
		return infraerrors.ServiceUnavailable("MODEL_EVALUATION_UNAVAILABLE", "Model evaluation is unavailable")
	}
	ctx := c.Request.Context()
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return err
	}
	if account == nil || !account.IsOpenAI() || (account.Type != AccountTypeOAuth && !account.IsCredentialShadow()) || account.IsSyntheticUITest() {
		return infraerrors.BadRequest("MODEL_EVALUATION_ACCOUNT_TYPE", "Model evaluation requires an OpenAI OAuth account")
	}
	if account.IsCredentialShadow() {
		parent, resolveErr := resolveCredentialAccount(ctx, s.accountRepo, account)
		if resolveErr != nil || parent == nil || parent.Type != AccountTypeOAuth || !parent.IsOpenAI() {
			return infraerrors.BadRequest("MODEL_EVALUATION_ACCOUNT_TYPE", "The shadow account requires an OpenAI OAuth parent")
		}
	}
	if strings.TrimSpace(request.ModelID) == "" {
		req.ModelID = defaultModelEvaluationModel(account)
	}
	mappedModel := account.GetMappedModel(req.ModelID)
	if isOpenAIImageModel(mappedModel) || !account.IsModelSupported(req.ModelID) {
		return infraerrors.BadRequest("MODEL_EVALUATION_MODEL_UNSUPPORTED", "Choose a supported text model for this account")
	}
	var lease *ModelEvaluationLease
	repository, supportsLeases := s.accountRepo.(ModelEvaluationRepository)
	if supportsLeases {
		lease, err = repository.ClaimModelEvaluation(ctx, accountID, time.Now(), modelEvaluationLeaseDuration(req.Runs, modelEvaluationTimeout))
		if err != nil {
			return err
		}
		if lease == nil {
			return infraerrors.Conflict("MODEL_EVALUATION_RUNNING", "An evaluation is already running for this account")
		}
		defer releaseModelEvaluationLease(repository, *lease, time.Time{})
	}
	if _, running := s.modelEvaluationRuns.LoadOrStore(accountID, struct{}{}); running {
		return infraerrors.Conflict("MODEL_EVALUATION_RUNNING", "An evaluation is already running for this account")
	}
	defer s.modelEvaluationRuns.Delete(accountID)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()
	var eventMu sync.Mutex
	send := func(event TestEvent) {
		eventMu.Lock()
		defer eventMu.Unlock()
		s.sendEvent(c, event)
	}
	stopHeartbeat := startModelEvaluationHeartbeat(ctx, 15*time.Second, func() {
		eventMu.Lock()
		defer eventMu.Unlock()
		_, _ = fmt.Fprint(c.Writer, ": keep-alive\n\n")
		c.Writer.Flush()
	})
	defer stopHeartbeat()
	for run := 1; run <= req.Runs; run++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		send(TestEvent{Type: "test_start", Model: mappedModel, Data: gin.H{"run": run, "runs": req.Runs, "kind": req.Kind}})
		result, err := s.runModelEvaluation(ctx, account, req, "manual", run, func(delta string) {
			send(TestEvent{Type: "content", Text: delta})
		})
		if err != nil {
			return err
		}
		var saveErr error
		if lease != nil {
			var applied bool
			applied, saveErr = repository.SaveModelEvaluationResult(ctx, *lease, &result, time.Time{}, false)
			if saveErr == nil && !applied {
				message := "the evaluation lease expired; reload and retry"
				send(TestEvent{Type: "error", Error: message})
				return errors.New(message)
			}
		} else {
			saveErr = s.saveModelEvaluationResult(ctx, accountID, result)
		}
		if saveErr != nil {
			send(TestEvent{Type: "status", Code: "evaluation_history_save_failed", Text: "The result could not be saved to account history"})
		}
		send(TestEvent{Type: "evaluation_result", Data: result})
	}
	send(TestEvent{Type: "test_complete", Success: true})
	return nil
}

// runModelEvaluation is shared by the HTTP stream and the bounded background
// workers. It never writes SSE itself and needs no response recorder or growing
// in-memory HTTP response. The read-only upstream path only uses Request.
func (s *AccountTestService) runModelEvaluation(ctx context.Context, account *Account, request ModelEvaluationRequest, source string, run int, emit func(string)) (ModelEvaluationResult, error) {
	started := time.Now()
	result := ModelEvaluationResult{ID: uuid.NewString(), Kind: request.Kind, Model: request.ModelID,
		Effort: request.Effort, Source: source, Status: "failed", Run: run, Runs: request.Runs,
		TestedAt: started.UTC().Format(time.RFC3339Nano)}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	req, err := normalizeModelEvaluationRequest(request)
	if err != nil {
		result.Error = "Invalid evaluation configuration"
		return result, nil
	}
	result.Kind, result.Effort, result.Runs = req.Kind, req.Effort, req.Runs
	if strings.TrimSpace(request.ModelID) == "" {
		req.ModelID = defaultModelEvaluationModel(account)
	}
	result.Model = account.GetMappedModel(req.ModelID)
	if isOpenAIImageModel(result.Model) || !account.IsModelSupported(req.ModelID) {
		result.Error = "The selected text model is not supported by this account"
		return result, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, modelEvaluationTimeout)
	defer cancel()
	requestContext := &gin.Context{Request: (&http.Request{Method: http.MethodPost, Header: make(http.Header)}).WithContext(probeCtx)}
	probeErr := s.testOpenAIAccountConnectionWithOptions(requestContext, account, req.ModelID, "", AccountTestModeDefault, openAIAccountTestOptions{
		readOnly: true, suppressStart: true,
		onCredentialAccount: func(owner *Account) {
			var proxyID *int64
			if owner.ProxyID != nil {
				id := *owner.ProxyID
				proxyID = &id
				if owner.Proxy != nil {
					result.ProxyName = owner.Proxy.Name
				}
			}
			result.ProxyID = &proxyID
		},
		prepare: func(payload map[string]any) {
			prompt := modelEvaluationCandyPrompt
			if req.Kind == "pelican" {
				prompt = modelEvaluationPelicanPrompt
			}
			payload["input"] = []map[string]any{{"role": "user", "content": []map[string]any{{"type": "input_text", "text": prompt}}}}
			payload["instructions"] = "Answer the user directly without using tools."
			payload["tools"] = []any{}
			payload["tool_choice"] = "none"
			payload["reasoning"] = map[string]any{"effort": req.Effort}
			if model, ok := payload["model"].(string); ok {
				result.Model = model
			}
		},
		processStream: func(_ *gin.Context, body io.Reader) error {
			return readModelEvaluationStream(body, &result, emit)
		},
	})
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		probeErr = errors.New("evaluation timed out after 10 minutes")
	}
	result.DurationMS = time.Since(started).Milliseconds()
	if probeErr == nil {
		probeErr = gradeModelEvaluation(&result)
	}
	if probeErr != nil {
		result.Status, result.Error = "failed", probeErr.Error()
	}
	return result, nil
}

func releaseModelEvaluationLease(repository ModelEvaluationRepository, lease ModelEvaluationLease, nextDue time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = repository.ReleaseModelEvaluation(ctx, lease, nextDue)
}

func defaultModelEvaluationModel(account *Account) string {
	candidates := []string{openai.DefaultTestModel}
	if account.IsCredentialShadow() && account.QuotaDimensionOrDefault() == QuotaDimensionSpark {
		candidates = append([]string{"gpt-5.3-codex-spark"}, candidates...)
	}
	for _, model := range openai.DefaultModels {
		candidates = append(candidates, model.ID)
	}
	var aliases []string
	for alias := range account.GetModelMapping() {
		if !strings.ContainsAny(alias, "*?") {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	candidates = append(candidates, aliases...)
	for _, candidate := range candidates {
		if account.IsModelSupported(candidate) && !isOpenAIImageModel(account.GetMappedModel(candidate)) {
			return candidate
		}
	}
	return openai.DefaultTestModel // The normal validation returns a useful error.
}

// stop joins the goroutine before the handler can release its writer. Both
// normal completion and cancellation therefore leave no asynchronous writes.
func startModelEvaluationHeartbeat(ctx context.Context, interval time.Duration, write func()) (stop func()) {
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				write()
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
		<-stopped
	}
}

func (s *AccountTestService) saveModelEvaluationResult(ctx context.Context, accountID int64, result ModelEvaluationResult) error {
	latest, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return err
	}
	if latest == nil {
		return ErrAccountNotFound
	}
	var history []ModelEvaluationResult
	if raw, err := json.Marshal(latest.Extra[ModelEvaluationResultsExtraKey]); err == nil {
		_ = json.Unmarshal(raw, &history)
	}
	history = append([]ModelEvaluationResult{result}, history...)
	if len(history) > modelEvaluationHistoryLimit {
		history = history[:modelEvaluationHistoryLimit]
	}
	// SVGs are much larger than candy answers. Bound the combined history too,
	// retaining the newest complete results instead of truncating SVG markup.
	for len(history) > 1 {
		raw, err := json.Marshal(history)
		if err != nil {
			return err
		}
		if len(raw) <= modelEvaluationHistoryMaxBytes {
			break
		}
		history = history[:len(history)-1]
	}
	// UpdateExtra uses an atomic JSONB merge, preserving concurrent quota and
	// other account updates. Only the diagnostic history key is written.
	return s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{ModelEvaluationResultsExtraKey: history})
}

type modelEvaluationResponse struct {
	Status string          `json:"status"`
	Error  json.RawMessage `json:"error"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage struct {
		InputTokens         int64 `json:"input_tokens"`
		OutputTokens        int64 `json:"output_tokens"`
		OutputTokensDetails struct {
			ReasoningTokens int64 `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
}

// Require an explicit successful terminal response. A partial answer containing
// 21 must not turn a broken stream or an upstream failure into a correct result.
func readModelEvaluationStream(body io.Reader, result *ModelEvaluationResult, emit func(string)) error {
	scanner := bufio.NewScanner(io.LimitReader(body, 4<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var eventData []string
	streamedBytes := 0
	process := func() (bool, error) {
		if len(eventData) == 0 {
			return false, nil
		}
		data := strings.Join(eventData, "\n")
		eventData = nil
		if data == "[DONE]" {
			return false, errors.New("stream ended before a completed response")
		}
		var event struct {
			Type     string                  `json:"type"`
			Delta    string                  `json:"delta"`
			Response modelEvaluationResponse `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return false, errors.New("invalid response event")
		}
		switch event.Type {
		case "response.output_text.delta":
			if streamedBytes+len(event.Delta) > modelEvaluationMaxAnswer {
				return false, errors.New("model answer exceeded 256 KB")
			}
			streamedBytes += len(event.Delta)
			if emit != nil && event.Delta != "" {
				emit(event.Delta)
			}
		case "response.failed", "response.incomplete", "error":
			return false, errors.New("upstream response failed or was incomplete")
		case "response.completed", "response.done":
			response := event.Response
			result.InputTokens = response.Usage.InputTokens
			result.OutputTokens = response.Usage.OutputTokens
			result.ReasoningTokens = response.Usage.OutputTokensDetails.ReasoningTokens
			if (response.Status != "" && response.Status != "completed") || (len(response.Error) > 0 && string(response.Error) != "null") {
				return false, errors.New("upstream response did not complete successfully")
			}
			var answer strings.Builder
			for _, item := range response.Output {
				if item.Type != "message" {
					continue
				}
				for _, part := range item.Content {
					if part.Type == "output_text" || part.Type == "text" {
						if answer.Len()+len(part.Text) > modelEvaluationMaxAnswer {
							return false, errors.New("model answer exceeded 256 KB")
						}
						_, _ = answer.WriteString(part.Text)
					}
				}
			}
			if strings.TrimSpace(answer.String()) == "" {
				return false, errors.New("model did not return a text answer")
			}
			result.Answer = answer.String()
			return true, nil
		}
		return false, nil
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if done, err := process(); done || err != nil {
				return err
			}
		} else if strings.HasPrefix(line, "data:") {
			eventData = append(eventData, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if errors.Is(scanner.Err(), bufio.ErrTooLong) {
		return errors.New("upstream response event line exceeded 1 MB")
	}
	if scanner.Err() != nil {
		return errors.New("response stream was interrupted")
	}
	if done, err := process(); done || err != nil {
		return err
	}
	return errors.New("stream ended before a completed response")
}

var standaloneCandyAnswer = regexp.MustCompile(`(^|[^0-9])21([^0-9]|$)`)
var evaluationSVG = regexp.MustCompile(`(?is)<svg\b[^>]*(?:/>|>.*?</svg\s*>)`)
var evaluationCSSURL = regexp.MustCompile(`(?is)url\s*\(([^)]*)\)`)

func gradeModelEvaluation(result *ModelEvaluationResult) error {
	if result.Kind == "candy" {
		result.Status = "wrong"
		if standaloneCandyAnswer.MatchString(result.Answer) {
			result.Status = "correct"
		}
		return nil
	}
	svg := evaluationSVG.FindString(result.Answer)
	if svg == "" {
		return errors.New("model answer did not contain an SVG image")
	}
	if err := validateEvaluationSVG(svg); err != nil {
		return err
	}
	result.SVG, result.Status = svg, "completed"
	return nil
}

func validateEvaluationSVG(svg string) error {
	decoder := xml.NewDecoder(strings.NewReader(svg))
	depth, roots := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("model returned invalid SVG markup")
		}
		switch value := token.(type) {
		case xml.Directive, xml.ProcInst:
			return errors.New("SVG directives are not supported")
		case xml.StartElement:
			name := strings.ToLower(value.Name.Local)
			if value.Name.Space != "" && value.Name.Space != "http://www.w3.org/2000/svg" {
				return errors.New("SVG contains unsupported element namespaces")
			}
			if depth == 0 {
				roots++
				if name != "svg" {
					return errors.New("model did not return an SVG root")
				}
			}
			depth++
			switch name {
			case "script", "foreignobject", "iframe", "object", "embed", "audio", "video":
				return errors.New("SVG contains unsupported active content")
			}
			for _, attr := range value.Attr {
				key, content := strings.ToLower(attr.Name.Local), strings.TrimSpace(strings.ToLower(attr.Value))
				if strings.HasPrefix(key, "on") || ((key == "href" || key == "src") && !strings.HasPrefix(content, "#")) {
					return errors.New("SVG contains scripts or external assets")
				}
				if key == "attributename" && (strings.Contains(content, "href") || content == "src" || content == "style" || strings.HasPrefix(content, "on")) {
					return errors.New("SVG contains unsupported attribute animation")
				}
				if (key == "style" || strings.Contains(content, "url")) && unsafeEvaluationCSS(content) {
					return errors.New("SVG contains external styling")
				}
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if unsafeEvaluationCSS(strings.ToLower(string(value))) {
				return errors.New("SVG contains external styling")
			}
		}
	}
	if roots != 1 || depth != 0 {
		return errors.New("model returned invalid SVG markup")
	}
	return nil
}

func unsafeEvaluationCSS(value string) bool {
	if strings.Contains(value, "@import") || strings.Contains(value, "javascript:") || strings.Contains(value, `\`) {
		return true
	}
	for _, match := range evaluationCSSURL.FindAllStringSubmatch(value, -1) {
		target := strings.Trim(strings.TrimSpace(match[1]), `"'`)
		if !strings.HasPrefix(target, "#") {
			return true
		}
	}
	return false
}
