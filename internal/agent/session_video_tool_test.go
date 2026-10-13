package agent

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// TestToolResultVideoTakesSessionDefaultAtReadTime pins what a video read by a
// tool (read_file on an .mp4) does with :video_config.
//
// The tool builds a bare VideoPart (llm.MediaContentPart leaves FPS/Res unset),
// and the session fills the session default as the tool result is produced —
// through OnToolOutput here, and again (idempotently) as the step's parts enter
// the record. So the video IS affected, but only by the default in effect at the
// moment it was read: a later :video_config change cannot rewrite it, which is
// the whole point of binding fps/res to the part.
func TestToolResultVideoTakesSessionDefaultAtReadTime(t *testing.T) {
	var out bytes.Buffer
	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: &out},
			modelService:  &modelService{videoFPS: 5, videoRes: 1},
		},
		sharedState: sharedState{outputBroken: atomic.Bool{}},
	}

	video := &llm.VideoPart{URI: "data:video/mp4;base64,AAAA"}
	if err := s.handleToolOutput("call_1", []llm.ContentPart{video}, nil, 7); err != nil {
		t.Fatalf("handleToolOutput: %v", err)
	}
	if video.FPS != 5 || video.Res != 1 {
		t.Fatalf("tool-result video fps/res = %d/%d, want the session default 5/1", video.FPS, video.Res)
	}
	if body := out.String(); !strings.Contains(body, `"fps":5`) {
		t.Errorf("the UF frame does not carry the video's settings: %q", body)
	}

	// A later default change leaves the already-read video alone.
	s.modelService.videoFPS, s.modelService.videoRes = 2, 0
	if video.FPS != 5 || video.Res != 1 {
		t.Errorf("the video followed the default: now %d/%d, want 5/1", video.FPS, video.Res)
	}
}

// TestToolResultVideoWithOwnSettingsIsNotOverwritten: a part that arrives with
// its own settings (an adapter-provided video, not a tool read) is left as it
// is — the default only fills a gap.
func TestToolResultVideoWithOwnSettingsIsNotOverwritten(t *testing.T) {
	var out bytes.Buffer
	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: &out},
			modelService:  &modelService{videoFPS: 5, videoRes: 1},
		},
		sharedState: sharedState{outputBroken: atomic.Bool{}},
	}

	video := &llm.VideoPart{URI: "data:video/mp4;base64,AAAA", FPS: 8, Res: 0}
	if err := s.handleToolOutput("call_1", []llm.ContentPart{video}, nil, 7); err != nil {
		t.Fatalf("handleToolOutput: %v", err)
	}
	if video.FPS != 8 || video.Res != 0 {
		t.Errorf("video fps/res = %d/%d, want its own 8/0", video.FPS, video.Res)
	}
}
