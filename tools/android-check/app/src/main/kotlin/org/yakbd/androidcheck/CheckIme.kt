package org.yakbd.androidcheck

import android.content.Context
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.Rect
import android.inputmethodservice.InputMethodService
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.View
import android.view.WindowInsets
import java.io.File

// A minimal on-screen keyboard: two rows of keys that commit fixed text. After every layout it writes each
// key's rectangle in screen coordinates to files/keys.txt ("output left top right bottom"), so the driver taps
// the centres with `input tap`. Physical key events are logged to files/ime-keys.txt and passed on unchanged.
class CheckIme : InputMethodService() {
    override fun onEvaluateInputViewShown(): Boolean {
        super.onEvaluateInputViewShown()
        return true
    }

    override fun onCreateInputView(): View = KeysView(this)

    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean {
        val dev = event.device?.name ?: "?"
        File(filesDir, "ime-keys.txt").appendText(
            "down ${KeyEvent.keyCodeToString(keyCode)} scan=${event.scanCode} unicode=${event.unicodeChar} device=$dev\n")
        return super.onKeyDown(keyCode, event)
    }

    inner class KeysView(context: Context) : View(context) {
        private val rows = listOf(listOf("ŝ", "ĝ", "ĉ", "ĥ"), listOf("a", "b", " ", "ŭ"))
        private val rects = mutableListOf<Pair<String, Rect>>()
        private val fill = Paint().apply { color = Color.LTGRAY }
        private val text = Paint().apply { color = Color.BLACK; textSize = 48f; textAlign = Paint.Align.CENTER }

        // Keys stay above the navigation bar, which may lie over the bottom of the keyboard window.
        private var navBottom = 0

        override fun onApplyWindowInsets(insets: WindowInsets): WindowInsets {
            val b = insets.getInsets(WindowInsets.Type.navigationBars()).bottom
            if (b != navBottom) {
                navBottom = b
                requestLayout()
            }
            return insets
        }

        override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
            val w = MeasureSpec.getSize(widthMeasureSpec)
            setMeasuredDimension(w, (resources.displayMetrics.density * 2 * 56).toInt() + navBottom)
        }

        override fun onLayout(changed: Boolean, left: Int, top: Int, right: Int, bottom: Int) {
            rects.clear()
            val rh = (height - navBottom) / rows.size
            for ((r, row) in rows.withIndex()) {
                val kw = width / row.size
                for ((k, out) in row.withIndex()) {
                    rects += out to Rect(k * kw + 4, r * rh + 4, (k + 1) * kw - 4, (r + 1) * rh - 4)
                }
            }
            post { report() }
        }

        private fun report() {
            val loc = IntArray(2)
            getLocationOnScreen(loc)
            val sb = StringBuilder()
            for ((out, rc) in rects) {
                sb.append("U+%04X %d %d %d %d\n".format(out.codePointAt(0),
                    rc.left + loc[0], rc.top + loc[1], rc.right + loc[0], rc.bottom + loc[1]))
            }
            File(filesDir, "keys.txt").writeText(sb.toString())
        }

        override fun onDraw(canvas: Canvas) {
            for ((out, rc) in rects) {
                canvas.drawRect(rc, fill)
                canvas.drawText(out, rc.exactCenterX(), rc.exactCenterY() + 16, text)
            }
        }

        override fun onTouchEvent(e: MotionEvent): Boolean {
            if (e.action == MotionEvent.ACTION_UP) {
                val x = e.x.toInt()
                val y = e.y.toInt()
                rects.firstOrNull { it.second.contains(x, y) }?.let {
                    currentInputConnection?.commitText(it.first, 1)
                }
            }
            return true
        }
    }
}
