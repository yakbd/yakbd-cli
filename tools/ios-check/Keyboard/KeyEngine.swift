import UIKit

// What a key does to the text, through the text document proxy only. A dead key types its own character at once;
// the next key replaces that character with the composed one when the text before the cursor still ends with it.
final class KeyEngine {
    let layout: CheckLayout
    private var pendingDead: String?

    init(layout: CheckLayout) {
        self.layout = layout
    }

    func type(_ text: String, into proxy: UITextDocumentProxy) {
        if let dead = pendingDead {
            pendingDead = nil
            let before = proxy.documentContextBeforeInput ?? ""
            if before.hasSuffix(dead) {
                if let composed = layout.deadKeys?[dead]?[text] {
                    proxy.deleteBackward()
                    proxy.insertText(composed)
                    return
                }
                if text == " " {
                    // Dead key then space: the dead key's own character, already in the text.
                    return
                }
            }
        }
        proxy.insertText(text)
        if layout.deadKeys?[text] != nil {
            pendingDead = text
        }
    }

    func backspace(_ proxy: UITextDocumentProxy) {
        pendingDead = nil
        proxy.deleteBackward()
    }
}
