"use strict";

const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
const app = document.getElementById("app");
const statusLine = document.getElementById("status");
const chat = document.getElementById("chat");
const chatLines = document.getElementById("chatlines");
const chatText = document.getElementById("chattext");
const chatNote = document.getElementById("chatnote");

const state = { view: null, denied: false, drafts: {}, notes: {}, busy: {}, seen: null, chatKey: "" };

function token() {
  const m = document.cookie.match(/(?:^|;\s*)deckhand_token=([^;]*)/);
  return m ? decodeURIComponent(m[1]) : "";
}

async function post(kind, body) {
  try {
    const r = await fetch("/api/" + kind, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Deckhand-Token": token() },
      body: JSON.stringify(body),
    });
    if (r.ok) return "";
    if (r.status === 403) return "The page has no key. Open it again from deckhand.";
    return (await r.text()).trim() || r.statusText;
  } catch {
    return "Deckhand does not answer.";
  }
}

function el(tag, props, ...kids) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v === null || v === undefined || v === false) continue;
    if (k === "class") n.className = v;
    else if (k === "text") n.textContent = v;
    else if (k === "value") n.value = v;
    else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v === true ? "" : v);
  }
  for (const c of kids.flat()) if (c !== null && c !== undefined && c !== false && c !== "") n.append(c);
  return n;
}

function group(t) {
  if (t.status === "needs-you") return 0;
  if (t.status === "todo") return 1;
  if (t.status === "waiting") return 3;
  return 2;
}

function plain(t) {
  return !t.project && !t.session;
}

function line(t) {
  if (t.need) return t.need;
  if (t.status === "waiting") return t.waits_on ? "Waits on " + t.waits_on : "Waiting";
  if (t.status === "needs-you") return "Needs you";
  if (t.status === "todo") return t.project ? "Not started" : "To do";
  return "Working";
}

function when(at) {
  const d = new Date(at);
  if (isNaN(d)) return "";
  const today = new Date().toDateString() === d.toDateString();
  return d.toLocaleString(undefined, today ? { hour: "2-digit", minute: "2-digit" } : { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}

function stamp(key) {
  const v = state.view;
  if (!v) return "";
  const id = key.slice(2);
  if (key.startsWith("s:")) {
    const s = (v.waiting || []).find((x) => x.id === id);
    return s ? s.need : "";
  }
  const t = (v.tasks || []).find((x) => x.id === id);
  return t ? [t.updated, t.status, t.need, (t.menu || []).join("\n")].join("|") : "";
}

function note(key) {
  const n = state.notes[key];
  if (!n) return el("p", { class: "note", role: "status" });
  return el("p", { class: n.error ? "note error" : "note", role: "status", text: n.text });
}

async function act(key, kind, body, okText) {
  state.busy[key] = true;
  delete state.notes[key];
  render();
  const err = await post(kind, body);
  delete state.busy[key];
  if (err) state.notes[key] = { error: true, text: err };
  else if (okText) state.notes[key] = { text: okText, stamp: stamp(key) };
  render();
  return err;
}

async function sendAnswer(key, target) {
  const text = (state.drafts[key] || "").trim();
  if (!text || state.busy[key]) return;
  const err = await act(key, "answer", { ...target, text }, "Sent, waiting for the session.");
  if (!err) {
    delete state.drafts[key];
    render();
  }
}

function answerForm(key, target, label) {
  const input = el("input", {
    type: "text",
    class: "field",
    "data-key": key,
    placeholder: label,
    "aria-label": label,
    autocomplete: "off",
    value: state.drafts[key] || "",
    readonly: !!state.busy[key],
    oninput: (e) => {
      state.drafts[key] = e.target.value;
      if (state.notes[key] && state.notes[key].error) {
        delete state.notes[key];
        e.target.closest("form").querySelector(".note").textContent = "";
      }
    },
  });
  return el(
    "form",
    { class: "answer", onsubmit: (e) => { e.preventDefault(); sendAnswer(key, target); } },
    el("div", { class: "fieldrow" }, input, el("button", { class: "button primary", type: "submit", "data-key": key + ":send", disabled: !!state.busy[key], text: "Send" })),
    note(key),
  );
}

function button(text, key, onclick, cls) {
  return el("button", { type: "button", class: "button" + (cls ? " " + cls : ""), "data-key": key, disabled: !!state.busy[key], onclick, text });
}

function prioTag(p) {
  return p === "urgent" || p === "high" ? el("span", { class: "tag " + p, text: p }) : null;
}

function taskRow(t) {
  const key = "t:" + t.id;
  return el(
    "li",
    { class: "row" },
    el(
      "a",
      { class: "rowmain", href: "#/task/" + encodeURIComponent(t.id), "data-key": "a:" + t.id },
      el("span", { class: "rowtop" }, el("span", { class: "title", text: t.title || t.id }), prioTag(t.prio), el("span", { class: "project", text: t.project_name || "" })),
      el("span", { class: "need", text: line(t) }),
    ),
    plain(t) ? el("div", { class: "rowside" }, button("Done", key, () => act(key, "done", { task: t.id })), note(key)) : null,
  );
}

function sessionRow(s) {
  const key = "s:" + s.id;
  return el(
    "li",
    { class: "row session" },
    el(
      "div",
      { class: "rowmain" },
      el("span", { class: "rowtop" }, el("span", { class: "title", text: s.name || s.id }), el("span", { class: "project", text: "session" })),
      el("span", { class: "need", text: s.need }),
      el("div", { class: "rowform" }, answerForm(key, { session: s.id }, "Answer " + (s.name || "the session")), button("Open", key + ":open", () => act(key + ":open", "open", { session: s.id }))),
      note(key + ":open"),
    ),
  );
}

function section(name, rows, empty) {
  return el(
    "section",
    { class: "group" },
    el("h2", {}, name, rows.length ? el("span", { class: "count", text: String(rows.length) }) : null),
    rows.length ? el("ul", { class: "rows" }, rows) : el("p", { class: "empty", text: empty }),
  );
}

function listPage(v) {
  const ts = v.tasks || [];
  return [
    section("Needs you", [...ts.filter((t) => group(t) === 0).map(taskRow), ...(v.waiting || []).map(sessionRow)], "Nothing needs you."),
    section("To do", ts.filter((t) => group(t) === 1).map(taskRow), "Nothing to do."),
    section("Working", ts.filter((t) => group(t) === 2).map(taskRow), "Nothing is in work."),
    section("Waiting on others", ts.filter((t) => group(t) === 3).map(taskRow), "Nothing waits on others."),
  ];
}

function prLabel(url) {
  const m = /\/pull\/(\d+)/.exec(url);
  return m ? "PR #" + m[1] : "PR";
}

function taskPage(v, id) {
  const back = el("a", { class: "back", href: "#/", "data-key": "back", text: "All tasks" });
  const t = (v.tasks || []).find((x) => x.id === id);
  if (!t) return [back, el("p", { class: "empty", text: "This task is not open. It may be done." })];
  const key = "t:" + t.id;
  const meta = [t.project_name, (t.prio || "normal") + " prio", t.deadline && "due " + t.deadline, t.from && "from " + t.from].filter(Boolean).join(" · ");
  const pr = !t.pr ? null : /^https:\/\//i.test(t.pr) ? el("a", { href: t.pr, target: "_blank", rel: "noopener noreferrer", text: prLabel(t.pr) }) : el("span", { text: "PR: " + t.pr });
  const menu = (t.menu || []).slice(0, 9);
  let answer = null;
  if (t.session && menu.length) {
    answer = el(
      "div",
      { class: "answer" },
      t.screen ? el("pre", { class: "screen", text: t.screen }) : null,
      el("div", { class: "choices" }, menu.map((c, i) => el("button", {
        type: "button",
        class: "button choice",
        "data-key": key + ":c" + (i + 1),
        disabled: !!state.busy[key],
        onclick: () => act(key, "choice", { task: t.id, choice: i + 1 }, "Sent."),
      }, el("span", { class: "num", text: String(i + 1) }), c))),
      note(key),
    );
  } else if (t.session) {
    answer = answerForm(key, { task: t.id }, "Your answer");
  }
  const history = (t.history || []).slice(-10).reverse();
  return [
    back,
    el("h1", { class: "tasktitle", text: t.title || t.id }),
    el("p", { class: "meta", text: meta }),
    el("div", { class: "ask " + t.status }, el("span", { class: "asklabel", text: t.status === "needs-you" ? "It asks" : "State" }), el("p", { text: line(t) })),
    answer,
    el(
      "div",
      { class: "actions" },
      t.session ? button("Open session", key + ":open", () => act(key + ":open", "open", { task: t.id })) : null,
      plain(t) ? button("Done", key + ":done", () => act(key + ":done", "done", { task: t.id })) : null,
      note(key + ":open"),
      note(key + ":done"),
    ),
    pr ? el("p", { class: "facts" }, pr) : null,
    t.context ? el("section", { class: "block" }, el("h2", { text: "Context" }), el("p", { class: "context", text: t.context })) : null,
    history.length
      ? el("section", { class: "block" }, el("h2", { text: "History" }), el("ol", { class: "history" }, history.map((h) => el("li", {}, el("time", { datetime: h.at, text: when(h.at) }), el("span", { text: h.text })))))
      : null,
  ];
}

function route() {
  const m = /^#\/task\/(.+)$/.exec(location.hash);
  return m ? decodeURIComponent(m[1]) : "";
}

function render() {
  const a = document.activeElement;
  const key = a && app.contains(a) && a.dataset ? a.dataset.key : "";
  const sel = key && typeof a.selectionStart === "number" ? [a.selectionStart, a.selectionEnd] : null;
  let body;
  if (state.denied) body = [el("p", { class: "empty", text: "This page needs its key. Open it from deckhand." })];
  else if (!state.view) body = [el("p", { class: "empty", text: "Loading." })];
  else {
    const id = route();
    body = id ? taskPage(state.view, id) : listPage(state.view);
  }
  app.replaceChildren(...body.filter(Boolean));
  if (key) {
    const n = app.querySelector('[data-key="' + CSS.escape(key) + '"]');
    if (n) {
      n.focus({ preventScroll: true });
      if (sel && n.setSelectionRange) n.setSelectionRange(sel[0], sel[1]);
    }
  }
}

function renderChat() {
  const lines = (state.view && state.view.chat) || [];
  const k = JSON.stringify(lines);
  if (k === state.chatKey) return;
  state.chatKey = k;
  const atEnd = chatLines.scrollHeight - chatLines.scrollTop - chatLines.clientHeight < 40;
  chatLines.replaceChildren(
    ...(lines.length
      ? lines.map((l) => el("li", { class: "line " + (l.from === "you" ? "you" : "them") }, el("span", { class: "who", text: (l.from === "you" ? "You" : "Assistant") + " " + when(l.at) }), el("p", { text: l.text })))
      : [el("li", { class: "empty", text: "Tell the assistant about a new thing, or ask a question." })]),
  );
  if (atEnd || !chat.open) chatLines.scrollTop = chatLines.scrollHeight;
}

function needKeys(v) {
  const out = new Map();
  for (const t of v.tasks || []) if (group(t) === 0) out.set("t:" + t.id, { title: t.title || t.id, body: line(t), hash: "#/task/" + encodeURIComponent(t.id) });
  for (const s of v.waiting || []) out.set("s:" + s.id, { title: s.name || s.id, body: s.need, hash: "#/" });
  return out;
}

function notify(v) {
  const now = needKeys(v);
  if (state.seen && "Notification" in window && Notification.permission === "granted" && !document.hasFocus()) {
    for (const [k, n] of now) {
      if (state.seen.has(k)) continue;
      const shown = new Notification(n.title, { body: n.body, tag: k });
      shown.onclick = () => {
        window.focus();
        location.hash = n.hash;
        shown.close();
      };
    }
  }
  state.seen = now;
}

function apply(v) {
  state.view = v;
  state.denied = false;
  document.title = v.count > 0 ? "(" + v.count + ") Tasks" : "Tasks";
  for (const [k, n] of Object.entries(state.notes)) if (n.stamp !== undefined && n.stamp !== stamp(k)) delete state.notes[k];
  notify(v);
  render();
  renderChat();
}

function openChat() {
  if (!chat.open) chat.showModal();
  chatLines.scrollTop = chatLines.scrollHeight;
  chatText.focus();
}

function toggleChat() {
  if (chat.open) chat.close();
  else openChat();
}

async function sendChat() {
  const text = chatText.value.trim();
  if (!text || chatText.readOnly) return;
  chatText.readOnly = true;
  chatNote.className = "note";
  chatNote.textContent = "Sending.";
  const err = await post("chat", { text });
  chatText.readOnly = false;
  if (err) {
    chatNote.className = "note error";
    chatNote.textContent = err;
    return;
  }
  chatText.value = "";
  chatNote.textContent = "Sent.";
  chatText.focus();
}

let events = null;

function connect() {
  if (events) events.close();
  events = new EventSource("/api/events");
  events.onopen = () => { statusLine.textContent = ""; };
  events.onmessage = (e) => {
    try {
      apply(JSON.parse(e.data));
    } catch {}
  };
  events.onerror = () => {
    statusLine.textContent = "Offline. Trying again.";
    if (events.readyState === EventSource.CLOSED) setTimeout(start, 3000);
  };
}

async function start() {
  let r;
  try {
    r = await fetch("/api/view", { headers: { "X-Deckhand-Token": token() }, cache: "no-store" });
  } catch {
    statusLine.textContent = "Offline. Trying again.";
    setTimeout(start, 3000);
    return;
  }
  if (r.status === 403) {
    state.denied = true;
    render();
    return;
  }
  if (r.ok) apply(await r.json());
  connect();
}

document.getElementById("askkey").textContent = isMac ? "⌘K" : "Ctrl K";
document.getElementById("ask").addEventListener("click", openChat);
document.getElementById("chatclose").addEventListener("click", () => chat.close());
chat.addEventListener("click", (e) => { if (e.target === chat) chat.close(); });
document.getElementById("chatform").addEventListener("submit", (e) => { e.preventDefault(); sendChat(); });
chatText.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
    e.preventDefault();
    sendChat();
  }
});
chatText.addEventListener("input", () => { if (chatNote.classList.contains("error")) chatNote.textContent = ""; });

document.addEventListener("keydown", (e) => {
  if ((isMac ? e.metaKey : e.ctrlKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === "k") {
    e.preventDefault();
    toggleChat();
  }
});

document.addEventListener("click", () => {
  if ("Notification" in window && Notification.permission === "default") Notification.requestPermission();
}, { once: true });

window.addEventListener("hashchange", () => {
  if (location.hash === "#new") {
    history.replaceState(null, "", location.pathname);
    openChat();
  }
  render();
  window.scrollTo(0, 0);
});

const opening = location.hash === "#new";
history.replaceState(null, "", location.pathname + (opening ? "" : location.hash));
render();
if (opening) openChat();
start();
