package llm

// Input modality filtering: the model's own declaration of what it accepts.
//
// There are two gates on media, and they are not the same gate. The protocol is
// a hard limit and lives in each provider — the Messages API has no audio or
// video block, Chat Completions has no document block — and what it cannot carry
// is degraded there, per request. This file holds the other one: one endpoint
// serving many models, where a model takes less than the protocol can carry. A
// text-only model behind an OpenAI-compatible URL is the common case; a vision
// model that takes images but no audio is another.
//
// It runs once, in the agent, before any provider sees the parts, so it can only
// narrow what the protocol gate would have allowed. Nothing here can widen it:
// declaring `audio` on an Anthropic model changes nothing, because the provider
// still has no block to put it in.
//
// Degrading rather than dropping is the decision docs/providers.md explains for
// audio and video on Anthropic. Dropping leaves the model to infer that a result
// came back and it should describe it; a note stating that nothing was delivered
// is what stops it describing media it never perceived. Sending the part anyway
// is worse than either: the server rejects the whole request, and since the part
// is persisted history, every later request in the session fails the same way.

import "strings"

// FitInputModalities replaces every media part the model does not declare with a
// text note, at the top level and nested inside a tool result.
//
// declared is the model's own list (model.conf input_modalities). nil means "not
// declared" and returns the parts untouched — the protocol decides, which is what
// every model.conf written without the key means. An empty non-nil list is a
// declaration too, so every media part is degraded: a model that takes no media.
// "text" may appear in the list and is never filtered, because a message is text
// and cannot be sent without it — which is also why an empty list and ["text"]
// put the model in the same state.
//
// It never mutates the parts it is handed, and returns the input unchanged when
// nothing needs degrading.
func FitInputModalities(contents []ContentPart, declared []string) []ContentPart {
	if declared == nil {
		return contents
	}
	allowed := make(map[string]bool, len(declared))
	for _, modality := range declared {
		allowed[modality] = true
	}
	if !anyUndeclaredModality(contents, allowed) {
		return contents
	}

	out := make([]ContentPart, len(contents))
	for i, p := range contents {
		if tr, ok := p.(*ToolOutputPart); ok {
			// A tool result's own slice has to be rebuilt too, or an undeclared
			// part would survive inside the copy.
			clone := *tr
			clone.Output = make([]ContentPart, len(tr.Output))
			for j, sub := range tr.Output {
				clone.Output[j] = degradeUndeclared(sub, allowed)
			}
			out[i] = &clone
			continue
		}
		out[i] = degradeUndeclared(p, allowed)
	}
	return out
}

// anyUndeclaredModality reports whether any media part, at the top level or
// nested inside a tool result, carries a modality the model does not declare.
// The walk exists so a request that needs no change is returned as-is instead of
// copied on every send.
func anyUndeclaredModality(contents []ContentPart, allowed map[string]bool) bool {
	for _, p := range contents {
		if kind, _, _, ok := mediaPart(p); ok && !allowed[kind] {
			return true
		}
		if tr, ok := p.(*ToolOutputPart); ok {
			for _, sub := range tr.Output {
				if kind, _, _, ok := mediaPart(sub); ok && !allowed[kind] {
					return true
				}
			}
		}
	}
	return false
}

// degradeUndeclared returns the part as it should go out: media the model does
// not declare becomes the note, everything else is returned unchanged. The
// replacement keeps the original's role and history ID — the wire groups parts
// by role, and a nested part inside a tool result carries its role for the same
// reason it always did.
func degradeUndeclared(p ContentPart, allowed map[string]bool) ContentPart {
	kind, uri, meta, ok := mediaPart(p)
	if !ok || allowed[kind] {
		return p
	}
	return &TextPart{Text: undeclaredMediaNote(kind, uri), ContentPartMeta: meta}
}

// mediaPart names the modality a part carries, using the four words a model.conf
// declaration is written in — "image", "video", "audio", "document" — along with
// its URI and meta. A part that is not media reports false.
//
// The words are literals rather than wire.KindImage and friends because this
// package imports nothing from the module (it is the bottom of the layering);
// the vocabulary itself is validated where a declaration enters, against
// protocol.PlaneKinds, and TestFitInputModalitiesEmptyDeclarationDegradesEverything
// fails if a part type stops matching its word.
func mediaPart(p ContentPart) (kind, uri string, meta ContentPartMeta, ok bool) {
	switch v := p.(type) {
	case *ImagePart:
		return "image", v.URI, v.ContentPartMeta, true
	case *VideoPart:
		return "video", v.URI, v.ContentPartMeta, true
	case *AudioPart:
		return "audio", v.URI, v.ContentPartMeta, true
	case *DocumentPart:
		return "document", v.URI, v.ContentPartMeta, true
	}
	return "", "", ContentPartMeta{}, false
}

// undeclaredMediaNote is the text a media part degrades to when the model does
// not accept its kind.
//
// The wording follows anthropicUnsupportedMediaBlock for the same reason: a terse
// "[image unsupported]" reads to a model as "a result came back", and it will
// describe what it thinks it saw. The note states flatly that nothing was
// delivered, and names the setting responsible so the fix is findable without
// reading the source. As there, an embedded data URI is described by its media
// type and never echoed: the base64 is the largest thing in the conversation and
// no model can use it.
func undeclaredMediaNote(kind, uri string) string {
	desc := strings.ToUpper(kind[:1]) + kind[1:]
	if uri != "" {
		if mediaType, _, ok := ParseDataURI(uri); ok {
			desc += " (" + mediaType + ")"
		} else {
			desc += " at " + uri
		}
	}
	return "[" + desc + ": this model's input_modalities does not include " + kind +
		" input, so the content was NOT delivered to you and you have not perceived it." +
		" Do not describe or quote it.]"
}
