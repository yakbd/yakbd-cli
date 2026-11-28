package org.yakbd.androidcheck

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.text.Editable
import android.text.TextWatcher
import android.view.inputmethod.InputMethodManager
import android.widget.EditText
import java.io.File

// One text field. Every change is written to files/text.txt, which the driver reads with run-as.
// Starting the activity again with the extra "clear" empties the field.
class MainActivity : Activity() {
    private lateinit var edit: EditText

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        edit = EditText(this)
        edit.contentDescription = "yakbd-check-text"
        edit.isSingleLine = false
        setContentView(edit)
        edit.addTextChangedListener(object : TextWatcher {
            override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
            override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) {}
            override fun afterTextChanged(s: Editable?) = save()
        })
        save()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        if (intent.getBooleanExtra("clear", false)) edit.setText("")
        focus()
    }

    override fun onResume() {
        super.onResume()
        focus()
    }

    private fun focus() {
        edit.requestFocus()
        edit.post {
            getSystemService(InputMethodManager::class.java).showSoftInput(edit, 0)
        }
    }

    private fun save() {
        File(filesDir, "text.txt").writeText(edit.text.toString())
    }
}
