import QtQuick
import QtQuick.Controls
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui
import "Model.js" as Model

// Bar widget and panel in one component, the way omarchy.agents does it. The
// bar shows one glyph+count per non-empty pile; the panel holds the drop
// zone, the top 3 of `2min`, the top 3 of `deadline`, and count-only chips
// for the rest. The full board is a separate overlay window (Board.qml).
// Every action shells out to the shedit CLI, and the daemon's `watch` stream
// is the only way state gets back in.
Panel {
  id: root
  moduleName: "jgonc.shedit"
  ipcTarget: "jgonc.shedit"
  manageIpc: true

  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color urgent: bar ? bar.urgent : Color.urgent
  readonly property color dim: Qt.darker(foreground, 1.4)
  readonly property color faint: Qt.darker(foreground, 1.6)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family
  readonly property string binary: String(setting("binary", "shedit") || "shedit")

  // ---- State fed by `shedit watch`.
  property var board: null
  property string streamError: ""
  property string lastError: ""
  property double nowMs: Date.now()

  readonly property bool pressing: Model.deadlinePressing(board, nowMs)

  // ---- Panel interaction state.
  property string editingId: ""
  property var pendingFiles: []
  // Set while any drag (files) is anywhere over the panel, so the drop zone
  // can light up even when the pointer isn't over it yet.
  property bool panelDragOver: false
  // Set while a task row is being dragged between piles. Distinct from
  // panelDragOver (files) so the two drag kinds never light up each other's
  // drop feedback.
  property string dragId: ""
  property string dragPile: ""

  readonly property bool inputFocused: input.activeFocus || editingId !== ""

  // ---- Actions ---------------------------------------------------------
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

  function dump() {
    var text = input.text.trim()
    if (text === "" && pendingFiles.length === 0) return
    run(Model.dumpArgv(text, pendingFiles))
    input.text = ""
    pendingFiles = []
  }

  function addFiles(paths) {
    var next = pendingFiles.slice()
    for (var i = 0; i < paths.length; i++) if (paths[i] && next.indexOf(paths[i]) < 0) next.push(paths[i])
    pendingFiles = next
  }

  function removeFile(index) {
    var next = pendingFiles.slice()
    next.splice(index, 1)
    pendingFiles = next
  }

  // `done` is a status, not a pile: moving into it is a `done` RPC, wired
  // separately at each drop site. This only ever re-piles.
  function moveItem(item, pile) {
    if (!item || item.pile === pile) return
    run(["move", item.id, pile])
  }

  // Search every pile plus done for an item by id (only editingId lookups
  // need this now, so a full scan each time is fine).
  function itemById(id) {
    if (!board) return null
    for (var i = 0; i < Model.PILES.length; i++) {
      var items = Model.pileItems(board, Model.PILES[i])
      for (var j = 0; j < items.length; j++) if (items[j].id === id) return items[j]
    }
    var done = board.done || []
    for (var k = 0; k < done.length; k++) if (done[k].id === id) return done[k]
    return null
  }

  // Every "+N more" link and count chip shells out to the (not-yet-existing)
  // board overlay via omarchy-shell, the same way BarWidget.qml's menu does.
  function toggleFor(pile) {
    if (root.bar) root.bar.run(Model.pileToggleCmd(pile))
  }

  function open() {
    controller.show()
    Qt.callLater(function() { input.forceActiveFocus() })
  }

  function close() {
    editingId = ""
    controller.hide()
  }

  onBoardChanged: {
    if (editingId !== "" && !itemById(editingId)) editingId = ""
  }

  // The inline editor takes focus while open; hand it back so Esc works again.
  onEditingIdChanged: if (editingId === "") keyCatcher.forceActiveFocus()

  Timer { interval: 60000; running: true; repeat: true; onTriggered: root.nowMs = Date.now() }

  // A plugin reload destroys this item but not its child processes.
  Component.onDestruction: {
    restartTimer.stop()
    watchProc.running = false
    actionProc.running = false
    pasteProc.running = false
  }

  // ---- Processes -------------------------------------------------------
  Process {
    id: watchProc
    command: [root.binary, "watch"]
    running: true
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
      if (root.streamError === "") root.streamError = "watch exited (" + code + ")"
      restartTimer.restart()
    }
  }

  Timer {
    id: restartTimer
    interval: 3000
    onTriggered: if (!watchProc.running) watchProc.running = true
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
    id: pasteProc
    command: ["bash", Qt.resolvedUrl("paste-image.sh").toString().replace("file://", "")]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: pasteProc.result = String(text || "").trim()
    }
    property string result: ""
    onExited: function(code) {
      if (code === 0 && result !== "") root.addFiles([result])
      else input.paste()
      result = ""
    }
  }

  // ---- Bar cluster -----------------------------------------------------
  implicitWidth: clusterWrap.implicitWidth
  implicitHeight: clusterWrap.implicitHeight

  readonly property var clusterModel: {
    var c = root.board ? root.board.counts : null
    var out = []
    if (c && c["2min"]) out.push({ icon: Model.ICON.twoMin, count: c["2min"], alarm: false, dimmed: false, tip: c["2min"] + " quick" })
    if (c && c.deadline) out.push({ icon: Model.ICON.deadline, count: c.deadline, alarm: root.pressing, dimmed: false, tip: c.deadline + " with a deadline" })
    if (c && c.other) out.push({ icon: Model.ICON.eventually, count: c.other, alarm: false, dimmed: true, tip: c.other + " eventually" })
    if (out.length === 0) out.push({ icon: Model.ICON.eventually, count: 0, alarm: root.streamError !== "", dimmed: true, tip: root.streamError || "Nothing on your plate" })
    return out
  }

  // Counts-only row: unsorted, eventually, done. No item lists, just chips.
  readonly property var chipModel: {
    var b = root.board
    return [
      { pile: "eventually", icon: Model.ICON.eventually, count: b && b.counts ? (b.counts.other || 0) : 0 },
      { pile: "unsorted", icon: Model.ICON.unsorted, count: b ? Model.pileItems(b, "unsorted").length : 0 },
      { pile: "done", icon: Model.ICON.done, count: b ? (b.done || []).length : 0 }
    ]
  }

  Item {
    id: clusterWrap
    implicitWidth: cluster.implicitWidth
    implicitHeight: cluster.implicitHeight
    width: implicitWidth
    height: implicitHeight

    Row {
      id: cluster
      spacing: 0
      Repeater {
        model: root.clusterModel
        WidgetButton {
          required property var modelData
          bar: root.bar
          text: modelData.icon + (modelData.count ? " " + modelData.count : "")
          active: modelData.alarm
          opacity: modelData.dimmed && !modelData.alarm ? 0.5 : 1
          horizontalMargin: 6
          tooltipText: modelData.tip
          onPressed: function(b) { root.toggle() }
        }
      }
    }

    // Hovering a drag (files or an in-progress item drag) over the bar
    // opens the panel so its drop zone becomes reachable.
    // TEMP DIAGNOSTIC: logging to confirm whether external DND even reaches
    // this layer-shell surface. Remove once confirmed either way.
    DropArea {
      anchors.fill: parent
      onEntered: { console.log("[shedit] bar DropArea entered, hasUrls=" + drag.hasUrls); if (!root.opened) root.open() }
    }
  }

  // ---- Panel -----------------------------------------------------------
  KeyboardPanel {
    id: panel
    anchorItem: cluster
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(400))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(680))

    // Panel-wide drop target: lets the drop zone light up (and lets a drop
    // land) anywhere over the panel, not just over the drop zone itself. It
    // sits behind the key catcher/content below, so it never intercepts
    // clicks on rows or chips.
    DropArea {
      anchors.fill: parent
      keys: []
      onEntered: if (root.dragId === "") root.panelDragOver = true
      onExited: root.panelDragOver = false
      onDropped: function(drop) {
        if (root.dragId !== "") return
        if (drop.hasUrls) {
          var paths = []
          for (var i = 0; i < drop.urls.length; i++) paths.push(Model.urlToPath(drop.urls[i]))
          root.addFiles(paths)
          drop.acceptProposedAction()
        } else if (drop.hasText) {
          input.text = (input.text ? input.text + "\n" : "") + drop.text
          drop.acceptProposedAction()
        }
        root.panelDragOver = false
        input.forceActiveFocus()
      }
    }

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      blocked: root.inputFocused

      onReturnRequested: input.forceActiveFocus()
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }

      Flickable {
        id: flick
        anchors.fill: parent
        contentWidth: width
        contentHeight: column.implicitHeight
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        flickableDirection: Flickable.VerticalFlick
        interactive: contentHeight > height && root.dragId === ""
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        Column {
          id: column
          width: flick.width
          spacing: Style.space(12)

          // ---------- Drop zone ----------
          Rectangle {
            id: dropZone
            width: parent.width
            implicitHeight: dropColumn.implicitHeight + Style.space(16)
            radius: Style.cornerRadius
            readonly property bool dragOver: (dropArea.containsDrag || root.panelDragOver) && root.dragId === ""
            readonly property bool hot: dragOver || input.activeFocus
            color: hot ? Style.hoverFillFor(root.foreground, Color.accent) : "transparent"
            border.width: 1
            border.color: dragOver ? Color.accent : Util.alpha(root.foreground, input.activeFocus ? 0.45 : 0.2)
            Behavior on border.color { ColorAnimation { duration: 60 } }
            Behavior on color { ColorAnimation { duration: 60 } }

            DropArea {
              id: dropArea
              anchors.fill: parent
              keys: []
              onDropped: function(drop) {
                if (root.dragId !== "") return
                if (drop.hasUrls) {
                  var paths = []
                  for (var i = 0; i < drop.urls.length; i++) paths.push(Model.urlToPath(drop.urls[i]))
                  root.addFiles(paths)
                  drop.acceptProposedAction()
                } else if (drop.hasText) {
                  input.text = (input.text ? input.text + "\n" : "") + drop.text
                  drop.acceptProposedAction()
                }
                input.forceActiveFocus()
              }
            }

            Column {
              id: dropColumn
              anchors.left: parent.left
              anchors.right: parent.right
              anchors.top: parent.top
              anchors.margins: Style.space(8)
              spacing: Style.space(6)

              TextArea {
                id: input
                width: parent.width
                wrapMode: TextEdit.Wrap
                textFormat: TextEdit.PlainText
                placeholderText: "Dump anything. Enter sends, Shift+Enter breaks a line."
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
                    root.dump(); event.accepted = true
                  } else if (event.key === Qt.Key_V && (event.modifiers & Qt.ControlModifier)) {
                    if (!pasteProc.running) pasteProc.running = true
                    event.accepted = true
                  } else if (event.key === Qt.Key_Escape) {
                    if (input.text === "" && root.pendingFiles.length === 0) keyCatcher.forceActiveFocus()
                    else { input.text = ""; root.pendingFiles = [] }
                    event.accepted = true
                  }
                }
              }

              // Staged attachments: names as removable pills.
              Flow {
                width: parent.width
                spacing: Style.space(6)
                visible: root.pendingFiles.length > 0
                Repeater {
                  model: root.pendingFiles
                  Rectangle {
                    required property string modelData
                    required property int index
                    implicitWidth: pillRow.implicitWidth + Style.space(12)
                    implicitHeight: pillRow.implicitHeight + Style.space(6)
                    radius: Style.cornerRadius
                    color: Style.selectedFillFor(root.foreground, Color.accent)
                    Row {
                      id: pillRow
                      anchors.centerIn: parent
                      spacing: Style.space(6)
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
                        text: Model.baseName(modelData)
                        color: root.foreground
                        font.family: root.fontFamily
                        font.pixelSize: Style.font.bodySmall
                        elide: Text.ElideMiddle
                        width: Math.min(implicitWidth, Style.space(180))
                        anchors.verticalCenter: parent.verticalCenter
                      }
                      PanelActionButton {
                        iconText: Model.ICON.close
                        tooltipText: "Remove"
                        foreground: root.dim
                        fontFamily: root.fontFamily
                        fontSize: Style.font.caption
                        size: Style.space(16)
                        anchors.verticalCenter: parent.verticalCenter
                        onClicked: root.removeFile(index)
                      }
                    }
                  }
                }
              }

              // Persistent hint: doubles as the drop hint and the paste hint,
              // and swaps to a release message while something is dragged
              // over the panel.
              Text {
                width: parent.width
                horizontalAlignment: Text.AlignHCenter
                textFormat: Text.PlainText
                text: dropZone.dragOver ? "Release to add" : (Model.ICON.attach + "  Drop / paste (Ctrl+V)")
                color: dropZone.dragOver ? root.foreground : root.faint
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
              }

              Item {
                width: parent.width
                height: sendButton.implicitHeight
                Button {
                  id: sendButton
                  anchors.right: parent.right
                  anchors.verticalCenter: parent.verticalCenter
                  text: "Dump"
                  foreground: root.foreground
                  fontSize: Style.font.bodySmall
                  enabled: input.text.trim() !== "" || root.pendingFiles.length > 0
                  opacity: enabled ? 1 : 0.4
                  onClicked: root.dump()
                }
              }
            }
          }

          // ---------- Errors ----------
          Row {
            width: parent.width
            spacing: Style.space(6)
            visible: root.lastError !== "" || root.streamError !== ""
            Text {
              textFormat: Text.PlainText
              text: Model.ICON.alert
              color: root.urgent
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
            }
            Text {
              textFormat: Text.PlainText
              width: parent.width - Style.space(24)
              text: root.lastError || root.streamError
              color: root.urgent
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              wrapMode: Text.Wrap
            }
          }

          // ---------- 2min / deadline: top 3 + "more" ----------
          Repeater {
            model: ["2min", "deadline"]
            Section {
              required property string modelData
              width: column.width
              pile: modelData
            }
          }

          // ---------- Counts: unsorted / eventually / done ----------
          PanelSeparator { width: parent.width; foreground: root.foreground }
          Row {
            width: parent.width
            spacing: Style.space(8)
            Repeater {
              model: root.chipModel
              PileChip {
                required property var modelData
                pile: modelData.pile
                icon: modelData.icon
                count: modelData.count
              }
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
        // The hot spot must NOT depend on the ghost's own size: the label (and
        // so the width) only becomes known after the drag activates, and a
        // width-relative hot spot would then jump the reported drag point half
        // a ghost to the right of the cursor -- enough to light up, and drop
        // into, the neighbouring chip. A fixed inset keeps the drag point
        // pinned to the cursor no matter how wide the title is.
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

  // ---- One count chip: icon + count, click toggles the board overlay ----
  component PileChip: Rectangle {
    id: chip
    required property string pile
    required property string icon
    required property int count
    // Any task drag in progress, other than one originating from this same
    // pile, makes this chip a valid target and marks it as such.
    readonly property bool dragAvailable: root.dragId !== "" && root.dragPile !== chip.pile
    readonly property bool dropHot: chipDrop.containsDrag && chip.dragAvailable
    implicitWidth: chipRow.implicitWidth + Style.space(16)
    implicitHeight: chipRow.implicitHeight + Style.space(8)
    radius: Style.cornerRadius
    color: (chip.dropHot || chipMouse.containsMouse) ? Style.hoverFillFor(root.foreground, Color.accent) : "transparent"
    border.width: 1
    border.color: chip.dropHot ? Color.accent : (chip.dragAvailable ? Util.alpha(Color.accent, 0.4) : Util.alpha(root.foreground, 0.2))
    Behavior on color { ColorAnimation { duration: 60 } }
    Behavior on border.color { ColorAnimation { duration: 90 } }

    MouseArea {
      id: chipMouse
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onClicked: root.toggleFor(chip.pile)
    }

    DropArea {
      id: chipDrop
      anchors.fill: parent
      keys: ["shedit/item"]
      onDropped: function(drop) {
        if (!chip.dragAvailable) return
        var it = root.itemById(root.dragId)
        if (!it) return
        if (chip.pile === "done") root.run(["done", it.id])
        else root.moveItem(it, chip.pile)
        drop.accept()
      }
    }

    Row {
      id: chipRow
      anchors.centerIn: parent
      spacing: Style.space(6)
      Text {
        textFormat: Text.PlainText
        text: chip.icon
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
        anchors.verticalCenter: parent.verticalCenter
      }
      Text {
        textFormat: Text.PlainText
        text: Model.CHIP_LABEL[chip.pile] || chip.pile.toUpperCase()
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
        anchors.verticalCenter: parent.verticalCenter
      }
      Text {
        textFormat: Text.PlainText
        text: String(chip.count)
        color: root.foreground
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
        anchors.verticalCenter: parent.verticalCenter
      }
    }
  }

  // ---- One pile: header, top 3 rows, "+N more" link ---------------------
  component Section: Column {
    id: section
    required property string pile
    readonly property var items: Model.pileItems(root.board, pile)
    readonly property var visibleItems: items.slice(0, 3)
    readonly property int moreCount: Math.max(0, items.length - 3)
    // Any task drag in progress, other than one originating from this same
    // pile, makes the header a valid drop target and marks it as such.
    readonly property bool dragAvailable: root.dragId !== "" && root.dragPile !== pile
    readonly property bool dropHot: headerDrop.containsDrag && section.dragAvailable
    spacing: Style.space(4)
    visible: items.length > 0

    PanelSeparator { width: parent.width; foreground: root.foreground }

    // Drop target is scoped to the header, not the whole section: rows are
    // clickable now (open the inline editor), so only the header lights up
    // and accepts a drop.
    Rectangle {
      id: headerRect
      width: parent.width
      implicitHeight: headerRow.implicitHeight + Style.space(4)
      radius: Style.cornerRadius
      color: section.dropHot ? Style.hoverFillFor(root.foreground, Color.accent) : "transparent"
      border.width: (section.dropHot || section.dragAvailable) ? 1 : 0
      border.color: section.dropHot ? Color.accent : Util.alpha(Color.accent, 0.4)
      Behavior on color { ColorAnimation { duration: 60 } }
      Behavior on border.color { ColorAnimation { duration: 90 } }

      DropArea {
        id: headerDrop
        anchors.fill: parent
        keys: ["shedit/item"]
        onDropped: function(drop) {
          if (!section.dragAvailable) return
          root.moveItem(root.itemById(root.dragId), section.pile)
          drop.accept()
        }
      }

      Row {
        id: headerRow
        width: parent.width
        spacing: Style.space(6)
        anchors.verticalCenter: parent.verticalCenter
        Text {
          textFormat: Text.PlainText
          text: Model.PILE_ICON[section.pile]
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          anchors.verticalCenter: parent.verticalCenter
        }
        PanelSectionHeader {
          width: parent.width - Style.space(20)
          text: Model.PILE_LABEL[section.pile] + "  " + section.items.length
          foreground: root.foreground
          fontFamily: root.fontFamily
          anchors.verticalCenter: parent.verticalCenter
        }
      }
    }

    Repeater {
      model: section.visibleItems
      ItemRow {
        required property var modelData
        width: section.width
        item: modelData
      }
    }

    Text {
      visible: section.moreCount > 0
      textFormat: Text.PlainText
      text: "+" + section.moreCount + " more"
      color: root.faint
      font.family: root.fontFamily
      font.pixelSize: Style.font.bodySmall
      leftPadding: Style.space(10)
      topPadding: Style.space(2)
      bottomPadding: Style.space(4)

      MouseArea {
        anchors.fill: parent
        cursorShape: Qt.PointingHandCursor
        onClicked: root.toggleFor(section.pile)
      }
    }
  }

  // ---- One item: title + a single meta line ----------------------------
  component ItemRow: CursorSurface {
    id: row
    required property var item
    readonly property bool editing: root.editingId === item.id
    readonly property bool overdue: Model.isOverdue(item, root.nowMs)

    hasCursor: rowMouse.containsMouse
    foreground: root.foreground
    accent: Color.accent
    opacity: root.dragId === item.id ? 0.35 : 1
    implicitHeight: rowBody.implicitHeight + (editor.visible ? editor.implicitHeight + Style.space(6) : 0)

    MouseArea {
      id: rowMouse
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.top: parent.top
      height: rowBody.implicitHeight
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      drag.target: ghost
      drag.threshold: Style.space(8)
      drag.smoothed: false

      onPressed: function(mouse) {
        var p = rowMouse.mapToItem(keyCatcher, mouse.x, mouse.y)
        ghost.x = p.x - ghost.grabInset
        ghost.y = p.y - ghost.grabInset
      }
      drag.onActiveChanged: {
        if (drag.active) { root.dragId = row.item.id; root.dragPile = row.item.pile; root.editingId = "" }
      }
      onReleased: function(mouse) {
        if (root.dragId === row.item.id) {
          ghost.Drag.drop()
          root.dragId = ""
          root.dragPile = ""
        }
      }
      onClicked: root.editingId = row.editing ? "" : row.item.id
    }

    Column {
      id: rowBody
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.top: parent.top
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(24)
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

    // Only quick action left in the widget; everything else lives in the board overlay.
    PanelActionButton {
      iconText: Model.ICON.done
      tooltipText: "Done"
      // rowMouse loses hover while the pointer is over this button, so track our own.
      property bool selfHover: false
      opacity: rowMouse.containsMouse || selfHover ? 1 : 0
      onHovered: isHovered => selfHover = isHovered
      foreground: root.dim
      hoverColor: Color.accent
      fontFamily: root.fontFamily
      fontSize: Style.font.bodySmall
      anchors.right: parent.right
      anchors.rightMargin: Style.space(6)
      anchors.top: parent.top
      anchors.topMargin: Style.space(4)
      onClicked: root.run(["done", row.item.id])

      Behavior on opacity { NumberAnimation { duration: 90 } }
    }

    Editor {
      id: editor
      visible: row.editing
      anchors.top: rowBody.bottom
      anchors.topMargin: Style.space(6)
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(10)
      item: row.item
    }
  }

  // ---- Inline editor: title, deadline, attachments ----------------------
  component Editor: Column {
    id: editor
    required property var item
    spacing: Style.space(6)

    onVisibleChanged: if (visible) {
      titleField.text = item.title || ""
      deadlineField.text = item.deadline_local || ""
      Qt.callLater(function() { titleField.forceActiveFocus(); titleField.selectAll() })
    }

    function handleKeys(event, commit) {
      if (event.key === Qt.Key_Escape) { root.editingId = ""; event.accepted = true }
      else if (event.key === Qt.Key_Return || event.key === Qt.Key_Enter) { commit(); event.accepted = true }
    }

    Row {
      width: parent.width
      spacing: Style.space(6)
      Text {
        textFormat: Text.PlainText
        text: "TITLE"
        width: Style.space(64)
        color: root.faint
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
        font.letterSpacing: 1
        anchors.verticalCenter: parent.verticalCenter
      }
      TextField {
        id: titleField
        width: parent.width - Style.space(70)
        placeholderText: "Title"
        foreground: root.foreground
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
        Keys.onPressed: function(event) {
          editor.handleKeys(event, function() {
            var t = titleField.text.trim()
            if (t !== "" && t !== (editor.item.title || "")) root.run(["edit", editor.item.id, "-title", t])
            deadlineField.forceActiveFocus()
          })
        }
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(6)
      Text {
        textFormat: Text.PlainText
        text: "DUE"
        width: Style.space(64)
        color: root.faint
        font.family: root.fontFamily
        font.pixelSize: Style.font.caption
        font.letterSpacing: 1
        anchors.verticalCenter: parent.verticalCenter
      }
      TextField {
        id: deadlineField
        width: parent.width - Style.space(70)
        placeholderText: "YYYY-MM-DD or YYYY-MM-DD HH:MM, empty clears"
        foreground: Model.validDeadline(text) ? root.foreground : root.urgent
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
        Keys.onPressed: function(event) {
          editor.handleKeys(event, function() {
            var d = deadlineField.text.trim()
            if (!Model.validDeadline(d)) return
            if (d !== (editor.item.deadline_local || "")) root.run(d === "" ? ["deadline", editor.item.id] : ["deadline", editor.item.id, d])
            root.editingId = ""
          })
        }
      }
    }

    // Attachments: list with detach, and a drop target to add more.
    Rectangle {
      width: parent.width
      implicitHeight: attachColumn.implicitHeight + Style.space(12)
      radius: Style.cornerRadius
      // A task drag passing over the editor must not light this up: only
      // file drags can attach.
      readonly property bool attachHot: attachDrop.containsDrag && root.dragId === ""
      color: attachHot ? Style.hoverFillFor(root.foreground, Color.accent) : "transparent"
      border.width: 1
      border.color: attachHot ? Color.accent : Util.alpha(root.foreground, 0.15)

      DropArea {
        id: attachDrop
        anchors.fill: parent
        keys: []
        onDropped: function(drop) {
          if (root.dragId !== "") return
          if (!drop.hasUrls) return
          var argv = ["attach", editor.item.id]
          for (var i = 0; i < drop.urls.length; i++) argv.push(Model.urlToPath(drop.urls[i]))
          root.run(argv)
          drop.acceptProposedAction()
        }
      }

      Column {
        id: attachColumn
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        anchors.margins: Style.space(6)
        spacing: Style.space(2)

        Repeater {
          model: editor.item.attachments || []
          Row {
            required property var modelData
            width: parent.width
            spacing: Style.space(6)
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
              width: parent.width - Style.space(48)
              text: modelData.name + (modelData.kind ? "  ·  " + modelData.kind : "")
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.bodySmall
              elide: Text.ElideMiddle
              anchors.verticalCenter: parent.verticalCenter
            }
            PanelActionButton {
              iconText: Model.ICON.close
              tooltipText: (editor.item.attachments || []).length > 1 ? "Detach" : "The last attachment cannot be detached"
              enabled: (editor.item.attachments || []).length > 1
              foreground: root.dim
              hoverColor: root.urgent
              fontFamily: root.fontFamily
              fontSize: Style.font.caption
              anchors.verticalCenter: parent.verticalCenter
              onClicked: root.run(["detach", modelData.id])
            }
          }
        }

        Text {
          textFormat: Text.PlainText
          text: attachDrop.containsDrag && root.dragId === "" ? "Drop to attach" : "Drop files here to attach"
          color: root.faint
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          topPadding: Style.space(2)
        }
      }
    }
  }
}
