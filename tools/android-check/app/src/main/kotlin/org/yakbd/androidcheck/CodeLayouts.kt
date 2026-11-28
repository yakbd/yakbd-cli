package org.yakbd.androidcheck

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

// Declares the keyboard layouts of res/xml/keyboard_layouts.xml; the system only reads its meta-data.
class CodeLayouts : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {}
}
