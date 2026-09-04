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
  edit:       "󰏫", // nf-md-pencil           U+F03EB
  trash:      "󰆴", // nf-md-delete           U+F01B4
  retry:      "󰑐", // nf-md-refresh          U+F0450
  attach:     "󰏢", // nf-md-paperclip        U+F03E2
  alert:      "󰀨", // nf-md-alert_circle     U+F0028
  brain:      "󰧑", // nf-md-brain            U+F09D1
  close:      "✕"
}

var PILES = ["2min", "deadline", "eventually", "unsorted"]

var PILE_LABEL = { "2min": "2 MIN", "deadline": "DEADLINE", "eventually": "EVENTUALLY", "unsorted": "UNSORTED" }

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

// Flat, ordered list of every open item shown, for j/k navigation.
function flatItems(board) {
  var out = []
  if (!board) return out
  for (var i = 0; i < PILES.length; i++) out = out.concat(pileItems(board, PILES[i]))
  return out
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
