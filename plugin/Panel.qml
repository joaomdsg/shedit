import QtQuick
import QtQuick.Controls
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui
import "Model.js" as Model

// Bar widget and panel in one component, the way omarchy.agents does it. The
// bar shows one glyph+count per non-empty pile; the panel holds a drop zone
// and the board. Every action shells out to the shedit CLI, and the daemon's
// `watch` stream is the only way state gets back in.
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
  readonly property int doneShown: Math.max(0, parseInt(setting("doneShown", 5), 10) || 0)

  // ---- State fed by `shedit watch`.
  property var board: null
  property string streamError: ""
  property string lastError: ""
  property double nowMs: Date.now()

  readonly property var flat: Model.flatItems(board)
  readonly property bool pressing: Model.deadlinePressing(board, nowMs)

  // ---- Panel interaction state. One selection shared by mouse and keys.
  property bool cursorActive: false
  property string selectedId: ""
  property string editingId: ""
  property string menuId: ""
  property string inviteItemId: ""
  property string deleteId: ""
  property string dragId: ""
  property string dragPile: ""
  property var pendingFiles: []

  readonly property bool inputFocused: input.activeFocus || editingId !== "" || inviteItemId !== "" || confirm.opened

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

  function moveItem(item, pile) {
    if (!item || item.pile === pile) return
    run(["move", item.id, pile])
    menuId = ""
    inviteItemId = item.id
  }

  function itemById(id) {
    for (var i = 0; i < flat.length; i++) if (flat[i].id === id) return flat[i]
    var done = board ? board.done : []
    for (var j = 0; j < done.length; j++) if (done[j].id === id) return done[j]
    return null
  }

  function selectedItem() { return itemById(selectedId) }

  function moveCursor(dy) {
    if (flat.length === 0) return
    var idx = -1
    for (var i = 0; i < flat.length; i++) if (flat[i].id === selectedId) idx = i
    idx = Math.max(0, Math.min(flat.length - 1, idx + dy))
    selectedId = flat[idx].id
    cursorActive = true
  }

  function askDelete(id) {
    deleteId = id
    confirm.opened = true
  }

  function closeEditors() {
    editingId = ""
    menuId = ""
    inviteItemId = ""
  }

  function open() {
    controller.show()
    Qt.callLater(function() { input.forceActiveFocus() })
  }

  function close() {
    closeEditors()
    controller.hide()
  }

  // Keep the reason invite from lingering forever: the next board that shows
  // the move already carries a reason, or the item vanished, dismisses it.
  onBoardChanged: {
    if (inviteItemId !== "") {
      var it = itemById(inviteItemId)
      if (!it || (it.last_move && it.last_move.reason)) inviteItemId = ""
    }
    if (editingId !== "" && !itemById(editingId)) editingId = ""
  }

  Timer { interval: 60000; running: true; repeat: true; onTriggered: root.nowMs = Date.now() }

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
  implicitWidth: cluster.implicitWidth
  implicitHeight: cluster.implicitHeight

  readonly property var clusterModel: {
    var c = root.board ? root.board.counts : null
    var out = []
    if (c && c["2min"]) out.push({ icon: Model.ICON.twoMin, count: c["2min"], alarm: false, dimmed: false, tip: c["2min"] + " quick" })
    if (c && c.deadline) out.push({ icon: Model.ICON.deadline, count: c.deadline, alarm: root.pressing, dimmed: false, tip: c.deadline + " with a deadline" })
    if (c && c.other) out.push({ icon: Model.ICON.eventually, count: c.other, alarm: false, dimmed: true, tip: c.other + " eventually" })
    if (out.length === 0) out.push({ icon: Model.ICON.eventually, count: 0, alarm: root.streamError !== "", dimmed: true, tip: root.streamError || "Nothing on your plate" })
    return out
  }

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

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      blocked: root.inputFocused

      onMoveRequested: function(dx, dy) { if (dy !== 0) root.moveCursor(dy) }
      onActivateRequested: { var it = root.selectedItem(); if (it) root.run(["done", it.id]) }
      onReturnRequested: input.forceActiveFocus()
      onCloseRequested: root.close()
      onDeleteRequested: { var it = root.selectedItem(); if (it) root.askDelete(it.id) }
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        var it = root.selectedItem()
        if (t === "e" && it) root.editingId = it.id
        else if (t === "m" && it) root.menuId = it.id
        else if (t === "i") input.forceActiveFocus()
      }

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

          // ---------- Hero ----------
          PanelHero {
            width: parent.width
            title: Model.heroTitle(root.board)
            meta: Model.heroMeta(root.board)
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconComponent: Component {
              Text {
                textFormat: Text.PlainText
                text: Model.ICON.brain
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.display
              }
            }
          }

          // ---------- Drop zone ----------
          Rectangle {
            id: dropZone
            width: parent.width
            implicitHeight: dropColumn.implicitHeight + Style.space(16)
            radius: Style.cornerRadius
            readonly property bool hot: dropArea.containsDrag || input.activeFocus
            color: hot ? Style.hoverFillFor(root.foreground, Color.accent) : "transparent"
            border.width: 1
            border.color: dropArea.containsDrag ? Color.accent : Util.alpha(root.foreground, input.activeFocus ? 0.45 : 0.2)
            Behavior on border.color { ColorAnimation { duration: 60 } }
            Behavior on color { ColorAnimation { duration: 60 } }

            DropArea {
              id: dropArea
              anchors.fill: parent
              keys: []
              onDropped: function(drop) {
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
                    if (input.text === "" && root.pendingFiles.length === 0) root.close()
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

              Item {
                width: parent.width
                height: sendButton.implicitHeight
                Text {
                  textFormat: Text.PlainText
                  text: dropArea.containsDrag ? "Drop to attach" : "Drop files here · Ctrl+V pastes an image"
                  color: root.faint
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.caption
                  anchors.left: parent.left
                  anchors.right: sendButton.left
                  anchors.rightMargin: Style.space(8)
                  anchors.verticalCenter: parent.verticalCenter
                  elide: Text.ElideRight
                }
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

          // ---------- Piles ----------
          Repeater {
            model: Model.PILES
            Section {
              required property string modelData
              width: column.width
              pile: modelData
            }
          }

          // ---------- Done ----------
          Column {
            width: parent.width
            spacing: Style.space(4)
            visible: root.doneShown > 0 && root.board && root.board.done.length > 0

            PanelSeparator { width: parent.width; foreground: root.foreground }
            PanelSectionHeader {
              width: parent.width
              text: "DONE"
              foreground: root.foreground
              fontFamily: root.fontFamily
            }
            Repeater {
              model: root.board ? root.board.done.slice(0, root.doneShown) : []
              ItemRow {
                required property var modelData
                width: column.width
                item: modelData
                done: true
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
        width: ghostText.implicitWidth + Style.space(20)
        height: ghostText.implicitHeight + Style.space(10)
        radius: Style.cornerRadius
        color: Style.selectedFillFor(root.foreground, Color.accent)
        opacity: 0.85
        Drag.active: root.dragId !== ""
        Drag.keys: ["shedit/item"]
        Drag.hotSpot.x: width / 2
        Drag.hotSpot.y: height / 2
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

  ConfirmDialog {
    id: confirm
    parent: keyCatcher
    anchors.fill: parent
    message: "Delete this item and everything it holds?"
    confirmText: "Delete"
    foreground: root.foreground
    fontFamily: root.fontFamily
    onCanceled: { opened = false; root.deleteId = "" }
    onConfirmed: {
      if (root.deleteId) root.run(["delete", root.deleteId, "-y"])
      opened = false
      root.deleteId = ""
    }
  }

  // ---- One pile: header, drop target, rows ------------------------------
  component Section: Column {
    id: section
    required property string pile
    readonly property var items: Model.pileItems(root.board, pile)
    readonly property bool dropHot: sectionDrop.containsDrag && root.dragPile !== pile
    spacing: Style.space(4)
    visible: items.length > 0 || pile === "unsorted" || root.dragId !== ""

    PanelSeparator { width: parent.width; foreground: root.foreground }

    Rectangle {
      width: parent.width
      implicitHeight: body.implicitHeight
      radius: Style.cornerRadius
      color: section.dropHot ? Style.hoverFillFor(root.foreground, Color.accent) : "transparent"
      border.width: section.dropHot ? 1 : 0
      border.color: Color.accent
      Behavior on color { ColorAnimation { duration: 60 } }

      DropArea {
        id: sectionDrop
        anchors.fill: parent
        keys: ["shedit/item"]
        onDropped: function(drop) {
          var it = root.itemById(root.dragId)
          root.moveItem(it, section.pile)
          drop.accept()
        }
      }

      Column {
        id: body
        width: parent.width
        spacing: Style.space(2)

        Row {
          width: parent.width
          spacing: Style.space(6)
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
            text: Model.PILE_LABEL[section.pile] + (section.items.length ? "  " + section.items.length : "")
            foreground: root.foreground
            fontFamily: root.fontFamily
            anchors.verticalCenter: parent.verticalCenter
          }
        }

        Text {
          visible: section.items.length === 0
          textFormat: Text.PlainText
          text: root.dragId !== "" ? "Drop here" : "Nothing here"
          color: root.faint
          font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall
          leftPadding: Style.space(10)
          topPadding: Style.space(4)
          bottomPadding: Style.space(6)
        }

        Repeater {
          model: section.items
          ItemRow {
            required property var modelData
            width: body.width
            item: modelData
          }
        }
      }
    }
  }

  // ---- One item -------------------------------------------------------
  component ItemRow: CursorSurface {
    id: row
    required property var item
    property bool done: false
    readonly property bool selected: root.selectedId === item.id
    readonly property bool editing: root.editingId === item.id
    readonly property bool menuOpen: root.menuId === item.id
    readonly property bool inviting: root.inviteItemId === item.id && !!item.last_move && !item.last_move.reason
    readonly property bool failed: !!item.error
    readonly property bool overdue: Model.isOverdue(item, root.nowMs) && !done
    readonly property bool showActions: (rowMouse.containsMouse || (root.cursorActive && selected)) && root.dragId === ""

    hasCursor: root.cursorActive && selected && root.dragId === ""
    foreground: root.foreground
    accent: Color.accent
    opacity: done ? 0.55 : (root.dragId === item.id ? 0.35 : 1)
    implicitHeight: rowBody.implicitHeight + extras.implicitHeight + (extras.visible ? Style.space(6) : 0)

    MouseArea {
      id: rowMouse
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.top: parent.top
      height: rowBody.implicitHeight
      hoverEnabled: true
      acceptedButtons: Qt.LeftButton | Qt.RightButton
      cursorShape: Qt.PointingHandCursor
      drag.target: row.done ? null : ghost
      drag.threshold: Style.space(8)
      drag.smoothed: false

      onContainsMouseChanged: if (containsMouse) { root.cursorActive = true; root.selectedId = row.item.id }
      onPressed: function(mouse) {
        root.cursorActive = true
        root.selectedId = row.item.id
        var p = rowMouse.mapToItem(keyCatcher, mouse.x, mouse.y)
        ghost.x = p.x - ghost.width / 2
        ghost.y = p.y - ghost.height / 2
      }
      drag.onActiveChanged: {
        if (drag.active) { root.dragId = row.item.id; root.dragPile = row.item.pile; root.closeEditors() }
      }
      onReleased: function(mouse) {
        if (root.dragId === row.item.id) {
          ghost.Drag.drop()
          root.dragId = ""
          root.dragPile = ""
        }
      }
      onClicked: function(mouse) {
        if (mouse.button === Qt.RightButton) {
          if (!row.done) root.menuId = row.menuOpen ? "" : row.item.id
          return
        }
        if (row.done) return
        root.editingId = row.editing ? "" : row.item.id
        root.menuId = ""
      }
    }

    Item {
      id: rowBody
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.top: parent.top
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(6)
      implicitHeight: Math.max(info.implicitHeight, actions.implicitHeight) + Style.space(10)

      Column {
        id: info
        anchors.left: parent.left
        anchors.right: actions.visible ? actions.left : parent.right
        anchors.rightMargin: Style.space(8)
        anchors.verticalCenter: parent.verticalCenter
        spacing: Style.space(2)

        Row {
          width: parent.width
          spacing: Style.space(6)
          Text {
            visible: row.failed || row.item.processing
            textFormat: Text.PlainText
            text: row.failed ? Model.ICON.alert : Model.ICON.retry
            color: row.failed ? root.urgent : root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.body
            anchors.verticalCenter: parent.verticalCenter
            RotationAnimator on rotation {
              running: row.item.processing && !row.failed
              from: 0; to: 360; duration: 1200; loops: Animation.Infinite
            }
          }
          Text {
            textFormat: Text.PlainText
            width: parent.width - (row.failed || row.item.processing ? Style.space(22) : 0)
            text: Model.displayTitle(row.item)
            color: row.done ? root.dim : root.foreground
            font.family: root.fontFamily
            font.pixelSize: Style.font.body
            font.strikeout: row.done
            elide: Text.ElideRight
            anchors.verticalCenter: parent.verticalCenter
          }
        }

        Text {
          width: parent.width
          visible: text !== ""
          textFormat: Text.PlainText
          text: Model.subline(row.item)
          color: row.failed || row.overdue ? root.urgent : root.faint
          font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall
          elide: Text.ElideRight
          maximumLineCount: 1
        }
      }

      Row {
        id: actions
        anchors.right: parent.right
        anchors.verticalCenter: parent.verticalCenter
        spacing: Style.space(2)
        visible: row.showActions
        PanelActionButton {
          visible: row.failed
          iconText: Model.ICON.retry
          tooltipText: "Retry"
          foreground: root.dim
          hoverColor: root.foreground
          fontFamily: root.fontFamily
          onClicked: root.run(["retry", row.item.id])
        }
        PanelActionButton {
          visible: !row.done && !row.failed
          iconText: Model.ICON.edit
          tooltipText: "Edit"
          foreground: root.dim
          hoverColor: root.foreground
          fontFamily: root.fontFamily
          onClicked: { root.editingId = row.editing ? "" : row.item.id; root.menuId = "" }
        }
        PanelActionButton {
          iconText: row.done ? Model.ICON.reopen : Model.ICON.done
          tooltipText: row.done ? "Reopen" : "Done"
          foreground: root.dim
          hoverColor: root.foreground
          fontFamily: root.fontFamily
          onClicked: root.run([row.done ? "reopen" : "done", row.item.id])
        }
        PanelActionButton {
          iconText: Model.ICON.trash
          tooltipText: "Delete"
          foreground: root.dim
          hoverColor: root.urgent
          fontFamily: root.fontFamily
          onClicked: root.askDelete(row.item.id)
        }
      }
    }

    // Anything that expands under the row: pile menu, reason invite, editor.
    Column {
      id: extras
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.top: rowBody.bottom
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(10)
      spacing: Style.space(6)
      visible: row.menuOpen || row.inviting || row.editing

      Row {
        visible: row.menuOpen
        spacing: Style.space(4)
        Text {
          textFormat: Text.PlainText
          text: "MOVE TO"
          color: root.faint
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          font.letterSpacing: 1
          anchors.verticalCenter: parent.verticalCenter
          rightPadding: Style.space(4)
        }
        Repeater {
          model: Model.PILES
          Button {
            required property string modelData
            text: Model.PILE_ICON[modelData] + " " + modelData
            foreground: root.foreground
            fontSize: Style.font.caption
            selected: modelData === row.item.pile
            enabled: modelData !== row.item.pile
            opacity: enabled ? 1 : 0.4
            onClicked: root.moveItem(row.item, modelData)
          }
        }
      }

      Row {
        visible: row.inviting
        width: parent.width
        spacing: Style.space(6)
        TextField {
          id: reasonField
          width: parent.width - Style.space(24)
          placeholderText: "Why? One sentence, optional"
          foreground: root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall
          Keys.onPressed: function(event) {
            if (event.key === Qt.Key_Escape) { root.inviteItemId = ""; event.accepted = true }
            else if (event.key === Qt.Key_Return || event.key === Qt.Key_Enter) {
              var t = text.trim()
              if (t !== "" && row.item.last_move) root.run(["reason", row.item.last_move.id, t])
              root.inviteItemId = ""
              event.accepted = true
            }
          }
          onVisibleChanged: if (visible) { text = ""; forceActiveFocus() }
        }
        PanelActionButton {
          iconText: Model.ICON.close
          tooltipText: "Skip"
          foreground: root.dim
          fontFamily: root.fontFamily
          fontSize: Style.font.caption
          anchors.verticalCenter: parent.verticalCenter
          onClicked: root.inviteItemId = ""
        }
      }

      Editor { visible: row.editing; width: parent.width; item: row.item }
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
      color: attachDrop.containsDrag ? Style.hoverFillFor(root.foreground, Color.accent) : "transparent"
      border.width: 1
      border.color: attachDrop.containsDrag ? Color.accent : Util.alpha(root.foreground, 0.15)

      DropArea {
        id: attachDrop
        anchors.fill: parent
        keys: []
        onDropped: function(drop) {
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
          text: attachDrop.containsDrag ? "Drop to attach" : "Drop files here to attach"
          color: root.faint
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          topPadding: Style.space(2)
        }
      }
    }
  }
}
