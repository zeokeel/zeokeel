// Package reports is the structured-report backend (M4): the lead's answer when
// work is done, plus the delivery half that survives an undeliverable channel.
//
// Design rules that come from the product, not from ambition:
//   - a report without a goal is not a report, and a "pass" without evidence is
//     not a pass (both refused at write time);
//   - delivery is recorded, not assumed: a closed WeChat window, quiet hours or
//     no bound channel leave the report DEFERRED (in-app only) instead of
//     pretending it was announced;
//   - deferred reports are retried as ONE digest, so an unreadable channel does
//     not become a queue of messages that arrives at 3am all at once.
package reports

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Limits: a report is a summary, not a transcript.
const (
	MaxEvidence      = 20
	MaxOpenQuestions = 20
	MaxGoalLen       = 2000
	MaxRefLen        = 2000
	MaxRetained      = 200
	DefaultListLimit = 50
	// DefaultDigestAt is the local hour the deferred reports are digested.
	DefaultDigestAt = "08:00"
	// DefaultRetryInterval bounds how often a deferred report is retried
	// outside the digest hour.
	DefaultRetryInterval = 30 * time.Minute
)

// Config is the module's typed assembly config.
type Config struct {
	// Now overrides the clock (tests).
	Now func() time.Time `json:"-"`
	// ListLimit is the default page size.
	ListLimit int `json:"listLimit,omitempty"`
	// Retained bounds the stored reports (oldest dropped).
	Retained int `json:"retained,omitempty"`
	// Enabled starts the retry loop (default true).
	Enabled *bool `json:"enabled,omitempty"`
	// Interval overrides DefaultSweepInterval.
	Interval time.Duration `json:"interval,omitempty"`
	// DigestAt is the local time of day the accumulated deferred reports are
	// digested ("08:00"). A closed WeChat window cannot be pushed through, so the
	// product's answer is one summary at the hour the human is awake — not a
	// retry every minute.
	DigestAt string `json:"digestAt,omitempty"`
	// RetryInterval is how often a deferred report is retried outside the digest
	// hour (a channel that recovers mid-day should not wait until tomorrow).
	RetryInterval time.Duration `json:"retryInterval,omitempty"`
}

// enabled starts the retry loop (default true).
func (c Config) enabled() bool { return c.Enabled == nil || *c.Enabled }

// sweepInterval is how often deferred reports are retried.
func (c Config) sweepInterval() time.Duration {
	if c.Interval > 0 {
		return c.Interval
	}
	return DefaultSweepInterval
}

func (c Config) normalized() Config {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.ListLimit <= 0 {
		c.ListLimit = DefaultListLimit
	}
	if c.Retained <= 0 {
		c.Retained = MaxRetained
	}
	if strings.TrimSpace(c.DigestAt) == "" {
		c.DigestAt = DefaultDigestAt
	}
	if c.RetryInterval <= 0 {
		c.RetryInterval = DefaultRetryInterval
	}
	return c
}

// Dispatcher is the channel-dispatch half (contracts.ChannelDispatchService).
type Dispatcher interface {
	Deliver(ctx context.Context, category, text string) (contracts.DispatchReceipt, error)
}

// Service implements contracts.ReportService.
type Service struct {
	mu     sync.Mutex
	cfg    Config
	next   int
	rows   []contracts.Report // newest last
	notify Dispatcher
	logger contracts.Logger
	path   string
	// lastAttempt is when the sweep last tried the deferred set; a closed
	// channel must not be poked every minute.
	lastAttempt time.Time
	// lastDigestDay is the date (local) the digest already went out, so the
	// morning summary is once a day rather than once a sweep after 08:00.
	lastDigestDay string
}

// New builds an in-memory service.
func New(notify Dispatcher, logger contracts.Logger, cfg Config) *Service {
	return &Service{cfg: cfg.normalized(), notify: notify, logger: logger}
}

// WithNotifier attaches the dispatcher (used by tests and by module wiring,
// which resolves it lazily).
func (s *Service) WithNotifier(notify Dispatcher) *Service {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notify = notify
	return s
}

var _ contracts.ReportService = (*Service)(nil)

// List returns the newest reports first.
func (s *Service) List(ctx context.Context, limit int) ([]contracts.Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = s.cfg.ListLimit
	}
	out := make([]contracts.Report, 0, limit)
	for index := len(s.rows) - 1; index >= 0 && len(out) < limit; index-- {
		out = append(out, s.rows[index])
	}
	return out, nil
}

// Get returns one report.
func (s *Service) Get(ctx context.Context, id string) (contracts.Report, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.Report{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.rows {
		if row.ID == strings.TrimSpace(id) {
			return row, true, nil
		}
	}
	return contracts.Report{}, false, nil
}

// Put validates and stores one report.
func (s *Service) Put(ctx context.Context, report contracts.Report) (contracts.Report, error) {
	if err := ctx.Err(); err != nil {
		return contracts.Report{}, err
	}
	normalized, err := s.validate(report)
	if err != nil {
		return contracts.Report{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if normalized.ID == "" {
		s.next++
		normalized.ID = fmt.Sprintf("report-%d", s.next)
	}
	if normalized.At.IsZero() {
		normalized.At = s.cfg.Now()
	}
	// Replace-in-place keeps a re-submitted report one row, not two.
	replaced := false
	for index := range s.rows {
		if s.rows[index].ID == normalized.ID {
			s.rows[index] = normalized
			replaced = true
			break
		}
	}
	if !replaced {
		s.rows = append(s.rows, normalized)
	}
	if len(s.rows) > s.cfg.Retained {
		s.rows = s.rows[len(s.rows)-s.cfg.Retained:]
	}
	if err := s.persistLocked(); err != nil {
		return contracts.Report{}, err
	}
	return normalized, nil
}

// validate is the write-side guard.
func (s *Service) validate(report contracts.Report) (contracts.Report, error) {
	report.Goal = strings.TrimSpace(report.Goal)
	report.Verdict = strings.ToLower(strings.TrimSpace(report.Verdict))
	report.Author = strings.TrimSpace(report.Author)
	if report.Goal == "" {
		return contracts.Report{}, fmt.Errorf("%w: goal is required", contracts.ErrReportInvalid)
	}
	if len([]rune(report.Goal)) > MaxGoalLen {
		return contracts.Report{}, fmt.Errorf("%w: goal is longer than %d characters", contracts.ErrReportInvalid, MaxGoalLen)
	}
	switch report.Verdict {
	case contracts.ReportVerdictPass, contracts.ReportVerdictPartial, contracts.ReportVerdictFail:
	default:
		return contracts.Report{}, fmt.Errorf("%w: verdict must be pass, partial or fail", contracts.ErrReportInvalid)
	}
	if len(report.Evidence) > MaxEvidence {
		return contracts.Report{}, fmt.Errorf("%w: more than %d evidence rows", contracts.ErrReportInvalid, MaxEvidence)
	}
	evidence := make([]contracts.ReportEvidence, 0, len(report.Evidence))
	for _, item := range report.Evidence {
		item.Kind = strings.TrimSpace(item.Kind)
		item.Ref = strings.TrimSpace(item.Ref)
		item.Note = strings.TrimSpace(item.Note)
		if item.Kind == "" || item.Ref == "" {
			return contracts.Report{}, fmt.Errorf("%w: every evidence row needs kind and ref", contracts.ErrReportInvalid)
		}
		if len([]rune(item.Ref)) > MaxRefLen {
			return contracts.Report{}, fmt.Errorf("%w: evidence ref is longer than %d characters", contracts.ErrReportInvalid, MaxRefLen)
		}
		evidence = append(evidence, item)
	}
	report.Evidence = evidence
	// A "pass" with nothing to show is the exact claim the audit layer exists to
	// reject; refusing it here keeps the two halves of the product consistent.
	if report.Verdict == contracts.ReportVerdictPass && len(evidence) == 0 {
		return contracts.Report{}, fmt.Errorf("%w: a pass needs at least one evidence row", contracts.ErrReportInvalid)
	}
	if len(report.OpenQuestions) > MaxOpenQuestions {
		return contracts.Report{}, fmt.Errorf("%w: more than %d open questions", contracts.ErrReportInvalid, MaxOpenQuestions)
	}
	questions := make([]string, 0, len(report.OpenQuestions))
	for _, question := range report.OpenQuestions {
		if trimmed := strings.TrimSpace(question); trimmed != "" {
			questions = append(questions, trimmed)
		}
	}
	report.OpenQuestions = questions
	return report, nil
}

// Deliver sends one stored report through the notification router and records
// what actually happened.
func (s *Service) Deliver(ctx context.Context, id string) (contracts.ReportDelivery, error) {
	if err := ctx.Err(); err != nil {
		return contracts.ReportDelivery{}, err
	}
	s.mu.Lock()
	index := s.indexLocked(id)
	notify := s.notify
	var report contracts.Report
	if index >= 0 {
		report = s.rows[index]
	}
	s.mu.Unlock()
	if index < 0 {
		return contracts.ReportDelivery{}, contracts.ErrReportNotFound
	}
	if notify == nil {
		return contracts.ReportDelivery{}, fmt.Errorf("report: no notification router is assembled")
	}
	receipt, err := notify.Deliver(ctx, contracts.NotifyCategoryReport, reportText(report))
	delivery := deliveryOf(report.ID, receipt, err)
	if err != nil {
		// A failed delivery must not lose the report: it stays deferred.
		delivery.Deferred = true
		delivery.Reason = err.Error()
	}
	s.mu.Lock()
	if delivery.Deferred {
		s.lastAttempt = s.cfg.Now()
	}
	if index := s.indexLocked(report.ID); index >= 0 {
		s.rows[index].Delivered = !delivery.Deferred && delivery.Channel != ""
		s.rows[index].Deferred = delivery.Deferred
		s.rows[index].DeferReason = delivery.Reason
	}
	persistErr := s.persistLocked()
	s.mu.Unlock()
	if persistErr != nil && s.logger != nil {
		s.logger.Printf("[reports] persist after delivery of %s: %v", report.ID, persistErr)
	}
	return delivery, nil
}

// Sweep retries the deferred reports as one digest.
func (s *Service) Sweep(ctx context.Context) (contracts.ReportSweep, error) {
	if err := ctx.Err(); err != nil {
		return contracts.ReportSweep{}, err
	}
	s.mu.Lock()
	pending := make([]contracts.Report, 0, 8)
	for _, row := range s.rows {
		if row.Deferred && !row.Delivered {
			pending = append(pending, row)
		}
	}
	notify := s.notify
	s.mu.Unlock()
	report := contracts.ReportSweep{Retried: len(pending)}
	if len(pending) == 0 {
		return report, nil
	}
	now := s.cfg.Now()
	s.mu.Lock()
	lastAttempt, lastDigestDay := s.lastAttempt, s.lastDigestDay
	s.mu.Unlock()
	digestDue := s.digestDue(now, lastDigestDay)
	if !digestDue && !lastAttempt.IsZero() && now.Sub(lastAttempt) < s.cfg.RetryInterval {
		// Inside the hold: the deferred set waits for the digest hour or for the
		// retry interval to pass. Retried=0 tells the caller nothing was sent.
		return contracts.ReportSweep{}, nil
	}
	_ = digestDue
	if notify == nil {
		report.Errors = append(report.Errors, "no notification router is assembled")
		return report, nil
	}
	// One digest, not N messages: the human was not told when each was ready, so
	// the useful thing to send now is "here is what accumulated".
	receipt, err := notify.Deliver(ctx, contracts.NotifyCategoryReport, digestText(pending))
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report, nil
	}
	delivery := deliveryOf("digest", receipt, nil)
	report.Digests = 1
	s.mu.Lock()
	s.lastAttempt = now
	if !delivery.Deferred && delivery.Channel != "" {
		s.lastDigestDay = now.Format("2006-01-02")
	}
	s.mu.Unlock()
	s.mu.Lock()
	for _, row := range pending {
		for index := range s.rows {
			if s.rows[index].ID != row.ID {
				continue
			}
			if !delivery.Deferred && delivery.Channel != "" {
				s.rows[index].Delivered = true
				s.rows[index].Deferred = false
				s.rows[index].DeferReason = ""
			} else {
				s.rows[index].DeferReason = "digest held: " + delivery.Reason
			}
			break
		}
	}
	if !delivery.Deferred {
		report.Delivered = len(pending)
	}
	persistErr := s.persistLocked()
	s.mu.Unlock()
	if persistErr != nil {
		report.Errors = append(report.Errors, persistErr.Error())
	}
	return report, nil
}

func (s *Service) indexLocked(id string) int {
	id = strings.TrimSpace(id)
	for index, row := range s.rows {
		if row.ID == id {
			return index
		}
	}
	return -1
}

// deliveryOf turns a dispatcher receipt into the report's view of it.
func deliveryOf(reportID string, receipt contracts.DispatchReceipt, err error) contracts.ReportDelivery {
	out := contracts.ReportDelivery{
		ReportID: reportID,
		Channel:  receipt.Channel,
		Attempts: receipt.Attempts,
		Errors:   receipt.Errors,
		Deferred: receipt.Deferred,
		Reason:   receipt.Reason,
	}
	// "inapp" only means nothing external took it: the human has not been told
	// yet, even when the router calls that success.
	if out.Channel == "" || out.Channel == contracts.NotifyChannelInApp {
		out.Deferred = true
		if out.Reason == "" {
			out.Reason = "no external channel accepted the report"
		}
	}
	if err != nil {
		out.Deferred = true
		if out.Reason == "" {
			out.Reason = err.Error()
		}
	}
	return out
}

// reportText is the human-facing message of one report.
func reportText(report contracts.Report) string {
	var builder strings.Builder
	builder.WriteString("任务报告：" + report.Goal + "\n")
	builder.WriteString("结论：" + verdictLabel(report.Verdict) + "\n")
	if len(report.Evidence) > 0 {
		builder.WriteString("证据：\n")
		for _, item := range report.Evidence {
			builder.WriteString("- " + item.Kind + ": " + item.Ref)
			if item.Note != "" {
				builder.WriteString("（" + item.Note + "）")
			}
			builder.WriteString("\n")
		}
	}
	if len(report.OpenQuestions) > 0 {
		builder.WriteString("待你决定：\n")
		for _, question := range report.OpenQuestions {
			builder.WriteString("- " + question + "\n")
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

// digestText is the accumulated message for deferred reports.
func digestText(reports []contracts.Report) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "有 %d 份报告此前未能送达，汇总如下：\n", len(reports))
	sorted := append([]contracts.Report(nil), reports...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })
	for _, report := range sorted {
		builder.WriteString("- [" + verdictLabel(report.Verdict) + "] " + report.Goal + "\n")
	}
	return strings.TrimRight(builder.String(), "\n")
}

func verdictLabel(verdict string) string {
	switch verdict {
	case contracts.ReportVerdictPass:
		return "完成"
	case contracts.ReportVerdictPartial:
		return "部分完成"
	case contracts.ReportVerdictFail:
		return "未完成"
	}
	return verdict
}

// errNoRows keeps gofmt honest about the errors import when validation messages
// are the only users.
var _ = errors.Is

// digestDue reports whether the morning digest is owed: today's digest hour has
// been reached and today's digest has not gone out yet.
func (s *Service) digestDue(now time.Time, lastDigestDay string) bool {
	hour, minute, ok := parseDigestAt(s.cfg.DigestAt)
	if !ok {
		return false
	}
	if now.Hour() < hour || (now.Hour() == hour && now.Minute() < minute) {
		return false
	}
	return lastDigestDay != now.Format("2006-01-02")
}

// parseDigestAt reads "HH:MM".
func parseDigestAt(value string) (int, int, bool) {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	hour, errHour := strconv.Atoi(strings.TrimSpace(parts[0]))
	minute, errMinute := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errHour != nil || errMinute != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}
