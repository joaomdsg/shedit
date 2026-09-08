import QtQuick
import QtQuick.Controls
import Quickshell
import Quickshell.Io
import Quickshell.Wayland
import qs.Commons
import qs.Ui
import "Model.js" as Model

// Full-board overlay: a separate top-level window from the bar widget/panel
// (Panel.qml), so it runs its own `shedit watch` process and keeps its own
// copy of the board. Piles on the left, detail + attachments on the right.
Item {
  id: root

  property var shell: null
  property bool opened: false

  // The bar widget (Panel.qml) already resolved this widget instance's
  // "binary" setting; this window has no settings of its own (overlay
  // plugins get no inline shell.json entry), so it reads the live bar
  // widget's resolved value instead of re-deriving it.
  readonly property string binary: {
    var widgets = (root.shell && root.shell.bar && typeof root.shell.bar.moduleWidgets === "function")
      ? root.shell.bar.moduleWidgets("jgonc.shedit") : []
    return (widgets && widgets.length && widgets[0].binary) ? String(widgets[0].binary) : "shedit"
  }

  readonly property color foreground: Color.menu.text
  readonly property color urgent: Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.4)
  readonly property color faint: Qt.darker(foreground, 1.6)
  readonly property string fontFamily: Style.font.family

  // ---- State fed by this window's own `shedit watch`.
  property var board: null
  property string streamError: ""
  property string lastError: ""
  property double nowMs: Date.now()

  // ---- Selection ---------------------------------------------------------
  readonly property var pileOrder: ["2min", "deadline", "eventually", "unsorted", "done"]
  property string selectedPile: "2min"
  property string selectedId: ""
  property string confirmDeleteId: ""

  readonly property var selectedItem: itemById(selectedId)

  onSelectedIdChanged: confirmDeleteId = ""

  function itemsForPile(pile) {
    if (pile === "done") return (root.board ? root.board.done || [] : [])
    return Model.pileItems(root.board, pile)
  }

  function pileLabel(pile) { return pile === "done" ? "DONE" : Model.PILE_LABEL[pile] }
  function pileIcon(pile) { return pile === "done" ? Model.ICON.done : Model.PILE_ICON[pile] }

  // Search every pile plus done for an item by id.
  function itemById(id) {
    if (!board || id === "") return null
    for (var i = 0; i < Model.PILES.length; i++) {
      var items = Model.pileItems(board, Model.PILES[i])
      for (var j = 0; j < items.length; j++) if (items[j].id === id) return items[j]
    }
    var done = board.done || []
    for (var k = 0; k < done.length; k++) if (done[k].id === id) return done[k]
    return null
  }

  function isDoneItem(id) {
    if (!board || id === "") return false
    var done = board.done || []
    for (var i = 0; i < done.length; i++) if (done[i].id === id) return true
    return false
  }

  function humanSize(n) {
    n = Number(n) || 0
    var units = ["B", "KB", "MB", "GB"]
    var i = 0
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++ }
    return (i === 0 ? String(n) : n.toFixed(1)) + " " + units[i]
  }

  // ---- Lifecycle ---------------------------------------------------------
  function open(payloadJson) {
    var payload = ({})
    try { payload = JSON.parse(payloadJson || "{}") } catch (e) { payload = ({}) }
    var pile = payload && typeof payload.pile === "string" ? payload.pile : ""
    if (Model.PILES.indexOf(pile) !== -1 || pile === "done") root.selectedPile = pile
    root.opened = true
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }

  function close() {
    root.opened = false
  }

  function toggle() {
    if (root.opened) root.dismiss()
    else root.open("{}")
  }

  // Escape / scrim click: route through the shell so openPanelIds stays in
  // sync, the same way the shell's own overlay plugins dismiss themselves.
  function dismiss() {
    if (root.shell && typeof root.shell.hide === "function") root.shell.hide("jgonc.shedit")
    else root.opened = false
  }

  onOpenedChanged: {
    if (root.opened) {
      if (!watchProc.running) watchProc.running = true
    } else {
      restartTimer.stop()
      watchProc.running = false
      selectedId = ""
    }
  }

  onBoardChanged: {
    if (selectedId !== "" && !itemById(selectedId)) selectedId = ""
  }

  Timer { interval: 60000; running: true; repeat: true; onTriggered: root.nowMs = Date.now() }

  // A plugin reload destroys this item but not its child processes.
  Component.onDestruction: {
    restartTimer.stop()
    watchProc.running = false
    actionProc.running = false
    snippetProc.running = false
    openProc.running = false
  }

  // ---- Actions ------------------------------------------------------------
  property var queue: []
  property string actionStderr: ""

  function run(argv) {
    queue.push(argv)
    pump()
  }

  function pump() {
    if (actionProc.running || queue.length === 0) return
    var argv = queue.shift()
    actionStderr = ""
    actionProc.command = [binary].concat(argv)
    actionProc.running = true
  }

  function moveItem(item, pile) {
    if (!item || item.pile === pile) return
    run(["move", item.id, pile])
  }

  // ---- Attachment preview snippets: at most one `head` process at a time,
  // cached by attachment id so a repaint never re-reads the same file.
  property var snippetCache: ({})
  property var snippetQueue: []
  property string snippetPendingId: ""

  function requestSnippet(att) {
    if (!att || (att.kind !== "text" && att.kind !== "url")) return
    if (root.snippetCache[att.id] !== undefined) return
    if (root.snippetPendingId === att.id) return
    for (var i = 0; i < root.snippetQueue.length; i++) if (root.snippetQueue[i].id === att.id) return
    root.snippetQueue = root.snippetQueue.concat([{ id: att.id, path: att.path }])
    pumpSnippets()
  }

  function pumpSnippets() {
    if (snippetProc.running || snippetQueue.length === 0) return
    var job = snippetQueue[0]
    snippetQueue = snippetQueue.slice(1)
    snippetPendingId = job.id
    snippetProc.command = ["head", "-c", "300", job.path]
    snippetProc.running = true
  }

  // ---- Opening links: at most one `xdg-open` at a time, and only ever for
  // strings that already look like a URL — never pass an arbitrary string.
  function openUrl(url) {
    var u = String(url || "")
    if (u.indexOf("http://") !== 0 && u.indexOf("https://") !== 0) return
    if (openProc.running) return
    openProc.command = ["xdg-open", u]
    openProc.running = true
  }

  // ---- Drag state: a task row dragged between piles. ----------------------
  property string dragId: ""
  property string dragPile: ""

  // ---- Processes -----------------------------------------------------------
  Process {
    id: watchProc
    command: [root.binary, "watch"]
    running: false
    stdout: SplitParser {
      onRead: function(line) {
        var b = Model.parseBoard(line)
        if (b) { root.board = b; root.streamError = "" }
      }
    }
    stderr: StdioCollector {
      waitForEnd: true
      onStreamFinished: { var t = String(text || "").trim(); if (t) root.streamError = t }
    }
    onExited: function(code) {
      if (!root.opened) return
      if (root.streamError === "") root.streamError = "watch exited (" + code + ")"
      restartTimer.restart()
    }
  }

  Timer {
    id: restartTimer
    interval: 3000
    onTriggered: if (root.opened && !watchProc.running) watchProc.running = true
  }

  Process {
    id: actionProc
    stderr: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.actionStderr = String(text || "").trim()
    }
    onExited: function(code) {
      root.lastError = code === 0 ? "" : (root.actionStderr || ("shedit exited " + code))
      root.pump()
    }
  }

  Process {
    id: snippetProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        var id = root.snippetPendingId
        var collapsed = String(text || "").replace(/\s+/g, " ").trim()
        var next = ({})
        for (var k in root.snippetCache) next[k] = root.snippetCache[k]
        next[id] = collapsed
        root.snippetCache = next
      }
    }
    onExited: function(code) {
      root.snippetPendingId = ""
      root.pumpSnippets()
    }
  }

  Process {
    id: openProc
    running: false
  }

  // ---- Window ---------------------------------------------------------------
  PanelWindow {
    id: win
    visible: root.opened
    anchors { top: true; bottom: true; left: true; right: true }
    color: "transparent"
    WlrLayershell.namespace: "omarchy-shedit-board"
    WlrLayershell.layer: WlrLayer.Overlay
    WlrLayershell.keyboardFocus: WlrKeyboardFocus.Exclusive
    exclusionMode: ExclusionMode.Ignore

    Rectangle {
      anchors.fill: parent
      color: Color.menu.scrim
    }

    MouseArea {
      anchors.fill: parent
      onClicked: root.dismiss()
    }

    BorderSurface {
      id: card
      width: Math.min(Style.space(1000), win.width - Style.gapsOut * 2)
      height: Math.min(Style.space(680), win.height - Style.gapsOut * 2)
      anchors.centerIn: parent
      radius: Style.cornerRadius
      color: Color.menu.background
      borderSpec: Border.surfaceSpec("menu", "border", Color.menu.border, Math.max(1, Style.space(2)))
      padding: Style.space(16)

      // Swallow clicks so they don't fall through to the scrim's dismiss.
      MouseArea { anchors.fill: parent; onClicked: {} }

      Item {
        id: keyCatcher
        anchors.fill: parent
        focus: true

        Keys.onPressed: function(event) {
          if (event.key === Qt.Key_Escape) { root.dismiss(); event.accepted = true }
        }

        Row {
          id: surface
          anchors.fill: parent
          spacing: Style.space(16)

          // ---------------- Left pane: piles + item list ----------------
          Item {
            id: leftPane
            width: Style.space(300)
            height: parent.height

            Column {
              id: pileButtonsCol
              anchors.top: parent.top
              anchors.left: parent.left
              anchors.right: parent.right
              spacing: Style.space(4)

              Repeater {
                model: root.pileOrder
                PileButton {
                  required property string modelData
                  width: pileButtonsCol.width
                  pile: modelData
                }
              }
            }

            PanelSeparator {
              id: pileSep
              anchors.top: pileButtonsCol.bottom
              anchors.topMargin: Style.space(8)
              width: parent.width
              foreground: root.foreground
            }

            Flickable {
              id: itemFlick
              anchors.top: pileSep.bottom
              anchors.topMargin: Style.space(8)
              anchors.left: parent.left
              anchors.right: parent.right
              anchors.bottom: parent.bottom
              clip: true
              contentWidth: width
              contentHeight: itemColumn.implicitHeight
              boundsBehavior: Flickable.StopAtBounds
              flickableDirection: Flickable.VerticalFlick
              interactive: contentHeight > height && root.dragId === ""
              ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

              Column {
                id: itemColumn
                width: itemFlick.width
                spacing: Style.space(2)

                Repeater {
                  model: root.itemsForPile(root.selectedPile)
                  ItemListRow {
                    required property var modelData
                    width: itemColumn.width
                    item: modelData
                  }
                }
              }
            }
          }

          // Vertical divider between the two panes.
          Rectangle {
            id: paneSep
            height: parent.height
            width: 1
            color: Qt.rgba(root.foreground.r, root.foreground.g, root.foreground.b, 0.12)
          }

          // ---------------- Right pane: detail ----------------
          Item {
            id: rightPane
            width: parent.width - leftPane.width - paneSep.width - surface.spacing * 2
            height: parent.height

            // Whole-pane file drop target; a task drag (dragId set) must
            // never light this up or be treated as an attach.
            DropArea {
              id: attachDrop
              anchors.fill: parent
              keys: []
              onDropped: function(drop) {
                if (root.dragId !== "" || !root.selectedItem) return
                if (!drop.hasUrls) return
                var argv = ["attach", root.selectedItem.id]
                for (var i = 0; i < drop.urls.length; i++) argv.push(Model.urlToPath(drop.urls[i]))
                root.run(argv)
                drop.acceptProposedAction()
              }
            }

            Text {
              anchors.centerIn: parent
              visible: !root.selectedItem
              textFormat: Text.PlainText
              text: "Select an item"
              color: root.faint
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
            }

            Flickable {
              id: detailFlick
              anchors.fill: parent
              visible: !!root.selectedItem
              clip: true
              contentWidth: width
              contentHeight: detailColumn.implicitHeight
              boundsBehavior: Flickable.StopAtBounds
              flickableDirection: Flickable.VerticalFlick
              interactive: contentHeight > height && root.dragId === ""
              ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

              DetailPane {
                id: detailColumn
                width: detailFlick.width
                item: root.selectedItem || ({})
              }
            }
          }
        }

        // Ghost that follows the pointer while a row is dragged between piles.
        Rectangle {
          id: ghost
          visible: root.dragId !== ""
          z: 100
          width: Math.min(ghostText.implicitWidth, Style.space(240)) + Style.space(20)
          height: ghostText.implicitHeight + Style.space(10)
          radius: Style.cornerRadius
          color: Style.selectedFillFor(root.foreground, Color.accent)
          opacity: 0.85
          Drag.active: root.dragId !== ""
          Drag.keys: ["shedit/item"]
          // Fixed inset, not width-relative: see Panel.qml's ghost for why a
          // width-bound hotSpot would jump the drag point away from the cursor.
          Drag.hotSpot.x: ghost.grabInset
          Drag.hotSpot.y: ghost.grabInset
          readonly property real grabInset: Style.space(10)
          Text {
            id: ghostText
            textFormat: Text.PlainText
            anchors.centerIn: parent
            text: { var it = root.itemById(root.dragId); return it ? Model.displayTitle(it) : "" }
            color: root.foreground
            font.family: root.fontFamily
            font.pixelSize: Style.font.body
            elide: Text.ElideRight
            width: Math.min(implicitWidth, Style.space(240))
          }
        }
      }
    }
  }

  // ---- One pile button: icon + label + count, drop target for re-piling ---
  component PileButton: Rectangle {
    id: btn
    required property string pile
    readonly property bool selectedNow: root.selectedPile === btn.pile
    readonly property bool dragAvailable: root.dragId !== "" && root.dragPile !== btn.pile
    readonly property bool dropHot: pileDrop.containsDrag && btn.dragAvailable

    implicitHeight: pileRow.implicitHeight + Style.space(16)
    radius: Style.cornerRadius
    color: dropHot ? Style.hoverFillFor(root.foreground, Color.accent)
      : (selectedNow ? Style.selectedFillFor(root.foreground, Color.accent)
        : (pileMouse.containsMouse ? Style.hoverFillFor(root.foreground, Color.accent) : "transparent"))
    border.width: (dropHot || (dragAvailable && !selectedNow)) ? 1 : 0
    border.color: dropHot ? Color.accent : Util.alpha(Color.accent, 0.4)
    Behavior on color { ColorAnimation { duration: 60 } }
    Behavior on border.color { ColorAnimation { duration: 90 } }

    MouseArea {
      id: pileMouse
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onClicked: root.selectedPile = btn.pile
    }

    DropArea {
      id: pileDrop
      anchors.fill: parent
      keys: ["shedit/item"]
      onDropped: function(drop) {
        if (!btn.dragAvailable) return
        var it = root.itemById(root.dragId)
        if (!it) return
        if (btn.pile === "done") root.run(["done", it.id])
        else root.moveItem(it, btn.pile)
        drop.accept()
      }
    }

    Item {
      id: pileRow
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(10)
      implicitHeight: Math.max(labelText.implicitHeight, countText.implicitHeight)

      Row {
        anchors.left: parent.left
        anchors.verticalCenter: parent.verticalCenter
        spacing: Style.space(8)
        Text {
          textFormat: Text.PlainText
          text: root.pileIcon(btn.pile)
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.body
          anchors.verticalCenter: parent.verticalCenter
        }
        Text {
          id: labelText
          textFormat: Text.PlainText
          text: root.pileLabel(btn.pile)
          color: root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall
          anchors.verticalCenter: parent.verticalCenter
        }
      }

      Text {
        id: countText
        anchors.right: parent.right
        anchors.verticalCenter: parent.verticalCenter
        textFormat: Text.PlainText
        text: String(root.itemsForPile(btn.pile).length)
        color: root.faint
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
      }
    }
  }

  // ---- One row in the selected pile's item list ----------------------------
  component ItemListRow: CursorSurface {
    id: row
    required property var item
    readonly property bool overdue: Model.isOverdue(item, root.nowMs)

    hasCursor: rowMouse.containsMouse
    current: root.selectedId === item.id
    foreground: root.foreground
    accent: Color.accent
    opacity: root.dragId === item.id ? 0.35 : 1
    implicitHeight: rowBody.implicitHeight

    MouseArea {
      id: rowMouse
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      drag.target: ghost
      drag.threshold: Style.space(8)
      drag.smoothed: false

      onPressed: function(mouse) {
        var p = rowMouse.mapToItem(surface, mouse.x, mouse.y)
        ghost.x = p.x - ghost.grabInset
        ghost.y = p.y - ghost.grabInset
      }
      drag.onActiveChanged: {
        if (drag.active) { root.dragId = row.item.id; root.dragPile = row.item.pile }
      }
      onReleased: function(mouse) {
        if (root.dragId === row.item.id) {
          ghost.Drag.drop()
          root.dragId = ""
          root.dragPile = ""
        }
      }
      onClicked: root.selectedId = row.item.id
    }

    Column {
      id: rowBody
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(10)
      topPadding: Style.space(4)
      bottomPadding: Style.space(4)
      spacing: Style.space(2)

      Text {
        width: parent.width
        textFormat: Text.PlainText
        text: Model.displayTitle(row.item)
        color: root.foreground
        font.family: root.fontFamily
        font.pixelSize: Style.font.body
        elide: Text.ElideRight
      }
      Text {
        width: parent.width
        visible: text !== ""
        textFormat: Text.PlainText
        text: Model.subline(row.item)
        color: row.overdue ? root.urgent : root.faint
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
        elide: Text.ElideRight
        maximumLineCount: 1
      }
    }
  }

  // ---- Right-pane detail for the selected item ------------------------------
  component DetailPane: Column {
    id: detail
    required property var item
    property bool editing: false
    // url attachments whose URL isn't already linked from within the
    // summary text (step 4); drives the link-chip row below the summary.
    readonly property var unreachedUrls: Model.unreachedUrlAttachments(detail.item, root.snippetCache)
    spacing: Style.space(10)

    onItemChanged: { syncFields(); editing = false }
    Component.onCompleted: syncFields()

    function syncFields() {
      if (!detail.item || !detail.item.id) return
      titleField.text = detail.item.title || ""
      summaryField.text = detail.item.summary || ""
      deadlineField.text = detail.item.deadline_local || ""
    }

    function commitTitle() {
      if (!detail.item || !detail.item.id) return
      var t = titleField.text.trim()
      if (t !== "" && t !== (detail.item.title || "")) root.run(["edit", detail.item.id, "-title", t])
      detail.editing = false
    }
    function revertTitle() {
      titleField.text = detail.item ? (detail.item.title || "") : ""
      detail.editing = false
    }

    function commitSummary() {
      if (!detail.item || !detail.item.id) return
      var s = summaryField.text
      if (s !== (detail.item.summary || "")) root.run(["edit", detail.item.id, "-summary", s])
      detail.editing = false
    }
    function revertSummary() {
      summaryField.text = detail.item ? (detail.item.summary || "") : ""
      detail.editing = false
    }

    function commitDeadline() {
      if (!detail.item || !detail.item.id) return
      var d = deadlineField.text.trim()
      if (!Model.validDeadline(d)) return
      if (d !== (detail.item.deadline_local || "")) root.run(d === "" ? ["deadline", detail.item.id] : ["deadline", detail.item.id, d])
    }
    function revertDeadline() { deadlineField.text = detail.item ? (detail.item.deadline_local || "") : "" }

    // ---- Title ----
    Row {
      width: parent.width
      spacing: Style.space(8)
      visible: !detail.editing

      Text {
        id: titleText
        width: parent.width - editButton.width - parent.spacing
        textFormat: Text.RichText
        text: Model.linkifyText(detail.item.title || "")
        color: root.foreground
        linkColor: Color.accent
        font.family: root.fontFamily
        font.pixelSize: Style.font.heading
        onLinkActivated: function(link) { root.openUrl(link) }

        MouseArea {
          anchors.fill: parent
          hoverEnabled: true
          acceptedButtons: Qt.NoButton
          cursorShape: containsMouse && parent.hoveredLink !== "" ? Qt.PointingHandCursor : Qt.ArrowCursor
        }
      }

      PanelActionButton {
        id: editButton
        iconText: Model.ICON.edit
        tooltipText: "Edit"
        foreground: root.dim
        fontFamily: root.fontFamily
        fontSize: Style.font.bodySmall
        anchors.verticalCenter: titleText.verticalCenter
        onClicked: { detail.editing = true; titleField.forceActiveFocus() }
      }
    }

    TextField {
      id: titleField
      visible: detail.editing
      width: parent.width
      placeholderText: "Title"
      foreground: root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.heading
      Keys.onPressed: function(event) {
        if (event.key === Qt.Key_Return || event.key === Qt.Key_Enter) {
          detail.commitTitle(); keyCatcher.forceActiveFocus(); event.accepted = true
        } else if (event.key === Qt.Key_Escape) {
          detail.revertTitle(); keyCatcher.forceActiveFocus(); event.accepted = true
        }
      }
      onActiveFocusChanged: if (!activeFocus) detail.commitTitle()
    }

    // ---- Summary ----
    Text {
      id: summaryText
      visible: !detail.editing
      width: parent.width
      textFormat: Text.RichText
      wrapMode: Text.WordWrap
      text: Model.linkifyText(detail.item.summary || "")
      color: root.foreground
      linkColor: Color.accent
      font.family: root.fontFamily
      font.pixelSize: Style.font.body
      onLinkActivated: function(link) { root.openUrl(link) }

      MouseArea {
        anchors.fill: parent
        hoverEnabled: true
        acceptedButtons: Qt.NoButton
        cursorShape: containsMouse && parent.hoveredLink !== "" ? Qt.PointingHandCursor : Qt.ArrowCursor
      }
    }

    // Chips for url attachments not otherwise reachable from the summary
    // text above (step 4); hidden entirely when there's nothing to show.
    Flow {
      width: parent.width
      spacing: Style.space(6)
      visible: detail.unreachedUrls.length > 0

      Repeater {
        model: detail.unreachedUrls
        Rectangle {
          id: chip
          required property var modelData
          radius: Style.cornerRadius
          color: chipMouse.containsMouse ? Style.hoverFillFor(root.foreground, Color.accent) : Util.alpha(Color.accent, 0.12)
          implicitWidth: chipText.implicitWidth + Style.space(16)
          implicitHeight: chipText.implicitHeight + Style.space(8)

          Text {
            id: chipText
            anchors.centerIn: parent
            textFormat: Text.PlainText
            text: Model.hostOf(root.snippetCache[chip.modelData.id])
            color: root.foreground
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
          }

          MouseArea {
            id: chipMouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: root.openUrl(String(root.snippetCache[chip.modelData.id]).trim())
          }
        }
      }
    }

    TextArea {
      id: summaryField
      visible: detail.editing
      width: parent.width
      wrapMode: TextEdit.Wrap
      textFormat: TextEdit.PlainText
      placeholderText: "Summary"
      placeholderTextColor: root.faint
      color: root.foreground
      selectionColor: Style.selectionFillFor(root.foreground, Color.accent)
      selectedTextColor: root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.body
      background: null
      padding: Style.space(2)
      Keys.onPressed: function(event) {
        if ((event.key === Qt.Key_Return || event.key === Qt.Key_Enter) && !(event.modifiers & Qt.ShiftModifier)) {
          detail.commitSummary(); keyCatcher.forceActiveFocus(); event.accepted = true
        } else if (event.key === Qt.Key_Escape) {
          detail.revertSummary(); keyCatcher.forceActiveFocus(); event.accepted = true
        }
      }
      onActiveFocusChanged: if (!activeFocus) detail.commitSummary()
    }

    // ---- Deadline ----
    Row {
      width: parent.width
      spacing: Style.space(6)
      Text {
        textFormat: Text.PlainText
        text: "DUE"
        width: Style.space(48)
        color: root.faint
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
        font.letterSpacing: 1
        anchors.verticalCenter: parent.verticalCenter
      }
      TextField {
        id: deadlineField
        width: parent.width - Style.space(54)
        placeholderText: "YYYY-MM-DD or YYYY-MM-DD HH:MM, empty clears"
        foreground: Model.validDeadline(text) ? root.foreground : root.urgent
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
        Keys.onPressed: function(event) {
          if (event.key === Qt.Key_Return || event.key === Qt.Key_Enter) {
            detail.commitDeadline(); keyCatcher.forceActiveFocus(); event.accepted = true
          } else if (event.key === Qt.Key_Escape) {
            detail.revertDeadline(); keyCatcher.forceActiveFocus(); event.accepted = true
          }
        }
        onActiveFocusChanged: if (!activeFocus) detail.commitDeadline()
      }
    }

    // ---- Read-only meta ----
    Flow {
      width: parent.width
      spacing: Style.space(10)
      Text {
        textFormat: Text.PlainText
        text: "Pile: " + (detail.item.pile || (root.isDoneItem(detail.item.id) ? "done" : ""))
        color: root.faint
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
      }
      Text {
        textFormat: Text.PlainText
        visible: !!detail.item.priority
        text: "Priority: " + detail.item.priority
        color: root.faint
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
      }
      Text {
        textFormat: Text.PlainText
        visible: !!detail.item.estimate
        text: "Estimate: " + detail.item.estimate
        color: root.faint
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
      }
      Text {
        textFormat: Text.PlainText
        visible: (detail.item.tags || []).length > 0
        text: "Tags: " + (detail.item.tags || []).join(", ")
        color: root.faint
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
      }
    }
    Text {
      width: parent.width
      visible: !!detail.item.reason
      textFormat: Text.PlainText
      text: detail.item.reason || ""
      color: root.faint
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
      wrapMode: Text.Wrap
    }

    PanelSeparator { width: parent.width; foreground: root.foreground }

    // ---- Controls ----
    Flow {
      width: parent.width
      spacing: Style.space(8)

      Button {
        text: root.isDoneItem(detail.item.id) ? "Reopen" : "Done"
        foreground: root.foreground
        fontSize: Style.font.bodySmall
        onClicked: root.run([root.isDoneItem(detail.item.id) ? "reopen" : "done", detail.item.id])
      }
      Button {
        text: "Re-extract"
        foreground: root.foreground
        fontSize: Style.font.bodySmall
        onClicked: root.run(["retry", detail.item.id])
      }
      Button {
        text: root.confirmDeleteId === detail.item.id ? "Confirm delete?" : "Delete"
        foreground: root.confirmDeleteId === detail.item.id ? root.urgent : root.foreground
        fontSize: Style.font.bodySmall
        onClicked: {
          if (root.confirmDeleteId === detail.item.id) {
            root.run(["delete", detail.item.id, "-y"])
            root.confirmDeleteId = ""
            root.selectedId = ""
          } else {
            root.confirmDeleteId = detail.item.id
          }
        }
      }
    }

    PanelSeparator { width: parent.width; foreground: root.foreground }
    PanelSectionHeader { text: "ATTACHMENTS"; foreground: root.foreground; fontFamily: root.fontFamily }

    Column {
      width: parent.width
      spacing: Style.space(6)

      Repeater {
        model: detail.item.attachments || []
        Column {
          id: attRow
          required property var modelData
          width: parent.width
          spacing: Style.space(2)

          readonly property bool isUrl: attRow.modelData.kind === "url"
          readonly property string urlValue: root.snippetCache[attRow.modelData.id] !== undefined
            ? String(root.snippetCache[attRow.modelData.id]).trim() : ""

          Component.onCompleted: root.requestSnippet(modelData)

          Row {
            id: attMainRow
            width: parent.width
            spacing: Style.space(6)

            // Opens the attachment's URL; anchored off the detach button so
            // the two click targets never overlap.
            MouseArea {
              visible: attRow.isUrl
              anchors.left: parent.left
              anchors.right: detachBtn.left
              anchors.top: parent.top
              anchors.bottom: parent.bottom
              cursorShape: Qt.PointingHandCursor
              onClicked: if (attRow.urlValue !== "") root.openUrl(attRow.urlValue)
            }

            Text {
              textFormat: Text.PlainText
              text: Model.ICON.attach
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              anchors.verticalCenter: parent.verticalCenter
            }
            Text {
              textFormat: Text.PlainText
              width: parent.width - Style.space(150)
              text: (attRow.isUrl ? (attRow.urlValue !== "" ? attRow.urlValue : attRow.modelData.name) : attRow.modelData.name)
                + "  ·  " + attRow.modelData.kind + "  ·  " + root.humanSize(attRow.modelData.size)
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              elide: Text.ElideMiddle
              anchors.verticalCenter: parent.verticalCenter
            }
            PanelActionButton {
              id: detachBtn
              iconText: Model.ICON.close
              tooltipText: (detail.item.attachments || []).length > 1 ? "Detach" : "The last attachment cannot be detached"
              enabled: (detail.item.attachments || []).length > 1
              foreground: root.dim
              hoverColor: root.urgent
              fontFamily: root.fontFamily
              fontSize: Style.font.caption
              anchors.verticalCenter: parent.verticalCenter
              onClicked: root.run(["detach", attRow.modelData.id])
            }
          }

          Text {
            visible: attRow.modelData.kind === "text" && !!root.snippetCache[attRow.modelData.id]
            width: parent.width
            leftPadding: Style.space(20)
            textFormat: Text.PlainText
            text: root.snippetCache[attRow.modelData.id] || ""
            color: root.faint
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
            elide: Text.ElideRight
            maximumLineCount: 1
          }
        }
      }

      Text {
        textFormat: Text.PlainText
        text: "Drop files here to attach"
        color: root.faint
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
      }
    }
  }
}
