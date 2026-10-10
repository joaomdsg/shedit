// Pure helpers for the shedit panel. No QML objects in here.
.pragma library

// Nerd Font (Material Design Icons) glyphs; codepoint noted per glyph.
var ICON = {
  twoMin:     "󰉁", // nf-md-flash            U+F0241
  deadline:   "󰃰", // nf-md-calendar_clock   U+F00F0
  eventually: "󰚇", // nf-md-inbox            U+F0687
  unsorted:   "󰘥", // nf-md-help_circle_outline U+F0625
  done:       "󰄬", // nf-md-check            U+F012C
  reopen:     "󰕌", // nf-md-undo             U+F054C
  trash:      "󰆴", // nf-md-delete           U+F01B4
  retry:      "󰑐", // nf-md-refresh          U+F0450
  attach:     "󰏢", // nf-md-paperclip        U+F03E2
  alert:      "󰀨", // nf-md-alert_circle     U+F0028
  brain:      "󰧑", // nf-md-brain            U+F09D1
  close:      "✕"
}

var PILES = ["2min", "deadline", "eventually", "unsorted"]

var PILE_LABEL = { "2min": "2 MIN", "deadline": "DEADLINE", "eventually": "EVENTUALLY", "unsorted": "UNSORTED" }

// Short forms for the count chips; "eventually" is abbreviated so all three fit one row.
var CHIP_LABEL = { "unsorted": "UNSORTED", "eventually": "LATER", "done": "DONE" }

var PILE_ICON = { "2min": ICON.twoMin, "deadline": ICON.deadline, "eventually": ICON.eventually, "unsorted": ICON.unsorted }

function parseBoard(line) {
  try {
    var b = JSON.parse(line)
    if (!b || typeof b !== "object") return null
    b.piles = b.piles || {}
    for (var i = 0; i < PILES.length; i++) b.piles[PILES[i]] = b.piles[PILES[i]] || []
    b.done = b.done || []
    b.failed = b.failed || []
    b.counts = b.counts || { "2min": 0, deadline: 0, other: 0 }
    return b
  } catch (e) {
    return null
  }
}

function pileItems(board, pile) {
  if (!board) return []
  var items = board.piles[pile] || []
  if (pile === "unsorted") items = items.concat(board.failed || [])
  return items
}

function isOverdue(item, nowMs) {
  if (!item || !item.deadline) return false
  var t = Date.parse(item.deadline)
  return !isNaN(t) && t < nowMs
}

// True when any deadline is overdue or falls within the current day.
function deadlinePressing(board, nowMs) {
  if (!board) return false
  var end = new Date(nowMs); end.setHours(23, 59, 59, 999)
  var items = board.piles.deadline || []
  for (var i = 0; i < items.length; i++) {
    var t = Date.parse(items[i].deadline || "")
    if (!isNaN(t) && t <= end.getTime()) return true
  }
  return false
}

function heroTitle(board) {
  if (!board) return "Connecting…"
  var n = (board.counts["2min"] || 0) + (board.counts.deadline || 0) + (board.counts.other || 0)
  if (n === 0) return "Nothing on your plate"
  return n + (n === 1 ? " thing" : " things") + " on your plate"
}

function heroMeta(board) {
  if (!board) return ""
  var c = board.counts
  var parts = []
  if (c["2min"]) parts.push(c["2min"] + " quick")
  if (c.deadline) parts.push(c.deadline + " due")
  if (c.other) parts.push(c.other + " later")
  if (board.failed && board.failed.length) parts.push(board.failed.length + " failed")
  return parts.join(" · ")
}

function displayTitle(item) {
  if (item.title) return item.title
  if (item.processing) return "Sorting…"
  if (item.error) return "Could not read this"
  var a = item.attachments && item.attachments[0]
  return a ? a.name : "Untitled"
}

function subline(item) {
  if (item.error) return item.error
  if (item.processing) return "extracting and sorting"
  var bits = []
  if (item.deadline_local) bits.push(item.deadline_local)
  if (item.estimate) bits.push(item.estimate)
  if (item.reason) bits.push(item.reason)
  return bits.join(" · ")
}

function baseName(path) {
  var i = path.lastIndexOf("/")
  return i >= 0 ? path.slice(i + 1) : path
}

// file:///a%20b -> /a b
function urlToPath(url) {
  var s = String(url)
  if (s.indexOf("file://") === 0) s = s.slice(7)
  try { return decodeURIComponent(s) } catch (e) { return s }
}

// Command string for a "+N more" link or count chip: shells out to the
// (not-yet-existing) board overlay, the same way BarWidget.qml's menu
// widget shells out to `omarchy-shell shell toggle omarchy.menu '{...}'`.
function pileToggleCmd(pile) {
  return "omarchy-shell shell toggle jgonc.shedit '" + JSON.stringify({ pile: pile }) + "'"
}

function dumpArgv(text, files) {
  var argv = ["dump"]
  for (var i = 0; i < files.length; i++) argv.push("-f", files[i])
  if (text) argv.push(text)
  return argv
}

// Deadline input: accept YYYY-MM-DD or "YYYY-MM-DD HH:MM"; empty clears.
function validDeadline(s) {
  s = s.trim()
  return s === "" || /^\d{4}-\d{2}-\d{2}( \d{2}:\d{2})?$/.test(s)
}

// HTML-escape plain text so it can be safely embedded as Qt RichText.
function escapeHtml(s) {
  return String(s || "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
}

// Turn plain text into Qt RichText with bare URLs wrapped in <a href>. The
// text is HTML-escaped *first* and the URL regex runs on the escaped
// result, so an anchor can never be built from (or injected via) raw
// markup in the source text — escaping happens before linking, never after,
// or a summary containing "<" could smuggle its own tags past us.
function linkifyText(text) {
  var escaped = escapeHtml(text)
  var re = /(https?:\/\/[^\s<]+|www\.[^\s<]+)/g
  return escaped.replace(re, function(match) {
    var trail = ""
    // Peel off trailing sentence punctuation (and an unbalanced closing
    // paren) so "see https://x.com." doesn't pull the period into the href.
    while (match.length > 0) {
      var last = match.charAt(match.length - 1)
      if (".,;:!?".indexOf(last) !== -1) {
        trail = last + trail
        match = match.slice(0, -1)
        continue
      }
      if (last === ")") {
        var opens = (match.match(/\(/g) || []).length
        var closes = (match.match(/\)/g) || []).length
        if (closes > opens) {
          trail = last + trail
          match = match.slice(0, -1)
          continue
        }
      }
      break
    }
    if (match === "") return trail
    var href = match.charAt(0) === "w" ? "https://" + match : match
    return "<a href=\"" + href + "\">" + match + "</a>" + trail
  })
}

// Host portion of a URL, for labelling link chips ("example.com").
function hostOf(url) {
  var s = String(url || "").trim()
  s = s.replace(/^https?:\/\//, "")
  var slash = s.indexOf("/")
  if (slash !== -1) s = s.slice(0, slash)
  return s
}

// url attachments of an item whose stored URL (once its snippet has loaded)
// does not already appear in the item's summary text, i.e. has no other
// on-screen link a user could click.
function unreachedUrlAttachments(item, snippetCache) {
  if (!item || !item.attachments) return []
  var summary = item.summary || ""
  var out = []
  var atts = item.attachments
  for (var i = 0; i < atts.length; i++) {
    var att = atts[i]
    if (att.kind !== "url") continue
    var url = snippetCache[att.id]
    if (url === undefined) continue
    url = String(url).trim()
    if (url === "" || summary.indexOf(url) !== -1) continue
    out.push(att)
  }
  return out
}
