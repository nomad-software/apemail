package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf16"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
	"github.com/mappu/miqt/qt6/webengine"

	"github.com/nomad-software/apemail/internal/images"
	"github.com/nomad-software/apemail/internal/message"
)

// bodyEditor is the body of a message being written: a page of Qt WebEngine,
// as messages are shown in (webview.go), that can be edited, as HTML or as
// plain text. The message it holds, as a quote, runs no scripts, and loads
// nothing from the internet but its images, as the message view does: those
// the view had loaded, and those fetched if the account loads images when
// a message is shown (webImage). The program's own script (editorScript) runs in
// a world of its own, which the page's cannot reach, and does what the
// window asks of it: Go asks by running a call of it, and it answers in the
// console, which only it can write to, as the page runs no scripts.
type bodyEditor struct {
	*webPage
	// html says the message is written as HTML, and not as plain text.
	html bool
	// doc is the page served, and loaded is set once it has loaded, until
	// which the calls of the script wait in pending.
	doc     string
	loaded  bool
	pending []string
	// modified says what is written has changed since it was set or saved.
	modified bool
	// answers wait for what the body holds (content), by the number each
	// is asked by; asked is the number last given.
	answers map[int]func(html, text string)
	asked   int
	// onState is told the format at the cursor when it changes.
	onState func(editState)
	// parts returns the pictures put in the message, which it shows as
	// cid:ID.
	parts func(id string) (message.Attachment, bool)

	// webImages are the images on the web that the body shows, by their
	// address: those the message view had loaded for the message quoted,
	// and those fetched for it if loadImages is set, as the account says
	// to load them when a message is shown. queued are those to be
	// fetched, tried those that have been, and stop stops them being
	// fetched, when the body is destroyed.
	webImages  map[string][]byte
	loadImages bool
	queued     []string
	tried      map[string]bool
	ctx        context.Context
	stop       context.CancelFunc
}

// editState is the format of the text at the cursor: bold, italic or
// underlined, and in a list of bullets or of numbers.
type editState struct {
	Bold, Italic, Underline, Bullets, Numbers bool
}

// bodyRoom is the room, in pixels, kept around what is written, so that it
// is not written up against the edges of the window.
const bodyRoom = 10

// cursorMark is the id of the element the cursor starts in, which the
// script takes off once it has put the cursor there.
const cursorMark = "apemail-cursor"

// consoleMark begins what the script writes to the console for the window.
const consoleMark = "apemail:"

// editorScript is the program's script in the body: it tells the window of
// changes and of the format at the cursor, and has what the window asks of
// it (apemail).
const editorScript = `(() => {
	const post = (m) => console.log("` + consoleMark + `" + JSON.stringify(m));
	let last = "";
	const state = () => {
		const s = {
			kind: "state",
			bold: document.queryCommandState("bold"),
			italic: document.queryCommandState("italic"),
			underline: document.queryCommandState("underline"),
			bullets: document.queryCommandState("insertUnorderedList"),
			numbers: document.queryCommandState("insertOrderedList"),
		};
		const json = JSON.stringify(s);
		if (json !== last) {
			last = json;
			post(s);
		}
	};
	document.addEventListener("selectionchange", state);
	document.addEventListener("input", () => {
		post({kind: "input"});
		state();
	});
	window.apemail = {
		content: (id) => post({kind: "content", id: id, html: document.body.innerHTML, text: document.body.innerText}),
		exec: (command, value) => {
			document.execCommand(command, false, value);
			state();
		},
		// retry asks again for the images of the addresses, which were
		// refused while they were being fetched.
		retry: (srcs) => {
			const wanted = new Set(srcs);
			for (const img of document.images) {
				if (wanted.has(img.src)) {
					img.setAttribute("src", img.getAttribute("src"));
				}
			}
		},
		colours: (css) => {
			document.getElementById("apemail-theme").textContent = css;
		},
		// start puts the cursor in the element marked, or at the offset
		// in the text of a body of plain text.
		start: (offset) => {
			document.body.focus();
			const r = document.createRange();
			const mark = document.getElementById("` + cursorMark + `");
			const text = document.body.firstChild;
			if (mark) {
				mark.removeAttribute("id");
				r.setStart(mark, 0);
			} else if (text && text.nodeType === Node.TEXT_NODE) {
				r.setStart(text, Math.min(offset, text.length));
			} else {
				r.setStart(document.body, 0);
			}
			r.collapse(true);
			getSelection().removeAllRanges();
			getSelection().addRange(r);
		},
	};
})();`

// newBodyEditor makes the body of a message written as HTML, or as plain
// text, which the parent owns.
func newBodyEditor(parent *qt.QObject, asHTML bool) *bodyEditor {
	e := &bodyEditor{html: asHTML, answers: map[int]func(string, string){}, webImages: map[string][]byte{}, tried: map[string]bool{}}
	e.ctx, e.stop = context.WithCancel(context.Background())
	e.webPage = newWebPage(e, parent, !asHTML)
	parent.OnDestroyed(e.stop)
	script := webengine.NewQWebEngineScript()
	script.SetName("editor")
	script.SetWorldId(uint(webengine.QWebEngineScript__ApplicationWorld))
	script.SetInjectionPoint(webengine.QWebEngineScript__DocumentReady)
	script.SetSourceCode(editorScript)
	e.page.Scripts().Insert(script)
	script.Delete()

	e.page.OnJavaScriptConsoleMessage(func(_ func(webengine.QWebEnginePage__JavaScriptConsoleMessageLevel, string, int, string), _ webengine.QWebEnginePage__JavaScriptConsoleMessageLevel, msg string, _ int, _ string) {
		// What else is written there is Chromium's own.
		if rest, ok := strings.CutPrefix(msg, consoleMark); ok {
			e.received(rest)
		}
	})
	// Nothing in the body is followed, nor anything loaded in its place.
	e.page.OnAcceptNavigationRequest(func(_ func(*qt.QUrl, webengine.QWebEnginePage__NavigationType, bool) bool, u *qt.QUrl, typ webengine.QWebEnginePage__NavigationType, mainFrame bool) bool {
		return mainFrame && encoded(u) == e.pageURL("edit") && typ != webengine.QWebEnginePage__NavigationTypeLinkClicked &&
			typ != webengine.QWebEnginePage__NavigationTypeFormSubmitted
	})
	e.page.OnLoadFinished(func(bool) {
		if e.gone || shownBy(encoded(e.page.Url())) != webHost(e) {
			return
		}
		e.loaded = true
		for _, s := range e.pending {
			e.page.RunJavaScript2(s, uint(webengine.QWebEngineScript__ApplicationWorld))
		}
		e.pending = nil
	})
	// A change of theme is shown at once, without loading the body again,
	// which would lose what is written.
	e.web.OnChangeEvent(func(super func(*qt.QEvent), ev *qt.QEvent) {
		super(ev)
		if ev.Type() == qt.QEvent__PaletteChange {
			e.paint()
			e.call("colours", themeCSS())
		}
	})
	e.paint()
	return e
}

// setHTML puts the HTML in the body, with the cursor at the start of the
// element marked cursorMark, if one is, or else at the start.
func (e *bodyEditor) setHTML(body string) {
	e.show(body, 0)
}

// setText puts the plain text in the body, with the cursor after the first
// cursor characters of it.
func (e *bodyEditor) setText(text string, cursor int) {
	before := []rune(text)[:min(cursor, len([]rune(text)))]
	e.show(html.EscapeString(text), len(utf16.Encode(before)))
}

// show loads a page of the body, with the cursor at the offset of
// start, and leaves it unmodified.
func (e *bodyEditor) show(body string, offset int) {
	// The room around what is written is the body's padding, by a rule
	// that outweighs even an important one of a message quoted for its
	// body, as newsletters have to take its margin and padding away. It is
	// within the body's width, as they make the body as wide as the
	// window, which room outside it would make wider, to scroll from side
	// to side. Its own margin is none, but where such a rule gives it one.
	editable := `contenteditable="true"`
	style := fmt.Sprintf(`:root > body { padding: %dpx !important; box-sizing: border-box !important } :where(body) { margin: 0 }`, bodyRoom)
	if !e.html {
		editable = `contenteditable="plaintext-only"`
		style += ` body { white-space: pre-wrap; font-family: monospace }`
	}
	e.doc = "<html><head>" + pageHead() + "<style>" + style + "</style></head><body " + editable + ">" + body + "</body></html>"
	e.loaded, e.pending, e.modified = false, nil, false
	e.load("edit")
	e.call("start", offset)
}

// call calls a function of the script with the arguments, once the body
// has loaded.
func (e *bodyEditor) call(name string, args ...any) {
	var list []string
	for _, a := range args {
		js, _ := json.Marshal(a)
		list = append(list, string(js))
	}
	s := "apemail." + name + "(" + strings.Join(list, ", ") + ")"
	if !e.loaded {
		e.pending = append(e.pending, s)
		return
	}
	e.page.RunJavaScript2(s, uint(webengine.QWebEngineScript__ApplicationWorld))
}

// content calls got with what the body holds, as HTML (as a page, if it is
// written as HTML) and as plain text, once the body has said.
func (e *bodyEditor) content(got func(html, text string)) {
	e.asked++
	e.answers[e.asked] = got
	e.call("content", e.asked)
}

// exec does an editing command to the body, as making the selection bold.
func (e *bodyEditor) exec(command, value string) {
	e.call("exec", command, value)
}

// received takes what the script says.
func (e *bodyEditor) received(msg string) {
	var m struct {
		Kind                                      string
		ID                                        int
		HTML, Text                                string
		Bold, Italic, Underline, Bullets, Numbers bool
	}
	if json.Unmarshal([]byte(msg), &m) != nil {
		return
	}
	switch m.Kind {
	case "input":
		e.modified = true
	case "state":
		if e.onState != nil {
			e.onState(editState{Bold: m.Bold, Italic: m.Italic, Underline: m.Underline, Bullets: m.Bullets, Numbers: m.Numbers})
		}
	case "content":
		got := e.answers[m.ID]
		if got == nil {
			return
		}
		delete(e.answers, m.ID)
		if !e.html {
			got("", m.Text)
			return
		}
		got(`<html><head><meta charset="utf-8"></head><body>`+m.HTML+"</body></html>", m.Text)
	}
}

// The Edit menu's commands, for the body (editor).

func (e *bodyEditor) Undo()      { e.page.TriggerAction(webengine.QWebEnginePage__Undo, false) }
func (e *bodyEditor) Redo()      { e.page.TriggerAction(webengine.QWebEnginePage__Redo, false) }
func (e *bodyEditor) Cut()       { e.page.TriggerAction(webengine.QWebEnginePage__Cut, false) }
func (e *bodyEditor) Copy()      { e.page.TriggerAction(webengine.QWebEnginePage__Copy, false) }
func (e *bodyEditor) Paste()     { e.page.TriggerAction(webengine.QWebEnginePage__Paste, false) }
func (e *bodyEditor) SelectAll() { e.page.TriggerAction(webengine.QWebEnginePage__SelectAll, false) }

// pastePlain pastes the text of the clipboard without its formatting.
func (e *bodyEditor) pastePlain() {
	e.page.TriggerAction(webengine.QWebEnginePage__PasteAndMatchStyle, false)
}

// canUndo and canRedo report if there is something to undo and to redo.
func (e *bodyEditor) canUndo() bool { return e.page.Action(webengine.QWebEnginePage__Undo).IsEnabled() }
func (e *bodyEditor) canRedo() bool { return e.page.Action(webengine.QWebEnginePage__Redo).IsEnabled() }

// base returns the page of the body.
func (e *bodyEditor) base() *webPage { return e.webPage }

// serve returns the page of the body, or an image of the web it shows.
func (e *bodyEditor) serve(u *url.URL) ([]byte, string, bool) {
	switch u.Path {
	case "/edit":
		return []byte(e.doc), "text/html; charset=utf-8", true
	case "/image":
		data, ok := e.webImages[u.Query().Get("src")]
		return data, http.DetectContentType(data), ok
	}
	return nil, "", false
}

// part returns a picture put in the message.
func (e *bodyEditor) part(id string) (message.Attachment, bool) {
	if e.parts == nil {
		return message.Attachment{}, false
	}
	return e.parts(id)
}

// webImage returns where an image of the web that the body has is served,
// and otherwise refuses it, and has it fetched if the account loads images
// when a message is shown, each once, no more than are fetched for a
// message.
func (e *bodyEditor) webImage(address string) string {
	if _, ok := e.webImages[address]; ok {
		return e.pageURL("image?src=" + url.QueryEscape(address))
	}
	if e.loadImages && !e.tried[address] && len(e.tried) < images.MaxImages {
		e.tried[address] = true
		e.queued = append(e.queued, address)
		// Those the page asks for together are fetched together, once
		// it has asked for them all.
		if len(e.queued) == 1 {
			mainthread.Start(e.fetchImages)
		}
	}
	return ""
}

// fetchImages fetches the images queued away from the UI's thread, and has
// the body ask for those fetched again, rather than loading it again, which
// would lose what is written.
func (e *bodyEditor) fetchImages() {
	srcs, ctx := e.queued, e.ctx
	e.queued = nil
	if e.gone || len(srcs) == 0 {
		return
	}
	go func() {
		got := images.Fetch(ctx, srcs)
		mainthread.Start(func() {
			if e.gone || ctx.Err() != nil || len(got) == 0 {
				return
			}
			maps.Copy(e.webImages, got)
			e.call("retry", slices.Collect(maps.Keys(got)))
		})
	}()
}
