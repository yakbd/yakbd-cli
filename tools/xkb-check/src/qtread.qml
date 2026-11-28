// qtread: a Qt Quick window with one text area; logs what was typed into it.
// Run with Qt's `qml` tool. Logs "XKBREAD READY" once the window is active and
// "XKBREAD TEXT <percent-encoded text>" after every change. MIT-0.
import QtQuick
import QtQuick.Window

Window {
    width: 600; height: 400; visible: true
    onActiveChanged: if (active) console.log("XKBREAD READY")
    TextEdit {
        anchors.fill: parent
        focus: true
        onTextChanged: console.log("XKBREAD TEXT " + encodeURIComponent(text))
    }
}
