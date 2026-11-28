package org.yakbd.androidcheck

import android.app.Activity
import android.app.Instrumentation
import android.hardware.input.InputManager
import android.os.Bundle
import android.view.InputDevice
import android.view.inputmethod.InputMethodInfo
import android.view.inputmethod.InputMethodManager
import android.view.inputmethod.InputMethodSubtype
import java.io.File
import java.lang.reflect.InvocationTargetException
import java.lang.reflect.Method

// Instrumentation with the shell's permissions (question 2b of README.md). Run with
//   am instrument -w --no-hidden-api-checks -e action dump|set -e device NAME[,NAME] [-e layout DESCRIPTOR]
//     org.yakbd.androidcheck/.LayoutTool
// "dump" prints every keyboard layout the system lists and, per named device, the layout the system chose for
// it under the check keyboard's one subtype; "set" calls the hidden setKeyboardLayoutForInputDevice first.
// The hidden methods are found by name, so one build runs on every API level; what was called is printed.
class LayoutTool : Instrumentation() {
    private val out = StringBuilder()
    private lateinit var args: Bundle

    override fun onCreate(arguments: Bundle?) {
        super.onCreate(arguments)
        args = arguments ?: Bundle()
        start()
    }

    override fun onStart() {
        super.onStart()
        val ui = uiAutomation
        ui.adoptShellPermissionIdentity()
        try {
            run()
        } catch (t: Throwable) {
            line("error ${describe(t)}")
        } finally {
            ui.dropShellPermissionIdentity()
        }
        File(targetContext.filesDir, "instr.txt").writeText(out.toString())
        val result = Bundle()
        result.putString(REPORT_KEY_STREAMRESULT, out.toString())
        finish(Activity.RESULT_OK, result)
    }

    private fun line(s: String) {
        out.append(s).append('\n')
    }

    private fun describe(t: Throwable): String {
        val c = if (t is InvocationTargetException && t.cause != null) t.cause!! else t
        return "${c.javaClass.name}: ${c.message}"
    }

    private fun method(target: Any, name: String, params: Int): Method? =
        target.javaClass.methods.firstOrNull { it.name == name && it.parameterCount == params }

    private fun run() {
        val ctx = targetContext
        val im = ctx.getSystemService(InputManager::class.java)
        val imm = ctx.getSystemService(InputMethodManager::class.java)
        val imi: InputMethodInfo? = imm.inputMethodList.firstOrNull { it.packageName == ctx.packageName }
        val subtype: InputMethodSubtype? = if (imi != null && imi.subtypeCount > 0) imi.getSubtypeAt(0) else null
        line("sdk ${android.os.Build.VERSION.SDK_INT}")
        line("ime ${imi?.id} subtype ${subtype?.languageTag} hash ${subtype?.hashCode()}")
        line("current-subtype ${imm.currentInputMethodSubtype?.languageTag}")
        try {
            val h = Class.forName("android.view.inputmethod.InputMethodSubtypeHandle")
                .getMethod("of", InputMethodInfo::class.java, InputMethodSubtype::class.java)
                .invoke(null, imi, subtype)
            line("subtype-handle ${h.javaClass.getMethod("toStringHandle").invoke(h)}")
        } catch (t: Throwable) {
            line("subtype-handle ? ${describe(t)}")
        }
        val all = method(im, "getKeyboardLayouts", 0)?.invoke(im) as Array<*>?
        for (kl in all ?: emptyArray<Any>()) {
            val d = kl!!.javaClass.getMethod("getDescriptor").invoke(kl)
            line("layout $d")
        }
        val names = (args.getString("device") ?: "").split(',').filter { it.isNotEmpty() }
        for (name in names) {
            val dev = InputDevice.getDeviceIds().map { InputDevice.getDevice(it) }
                .firstOrNull { it != null && it.name == name }
            if (dev == null) {
                line("device $name missing")
                continue
            }
            val id = InputDevice::class.java.getMethod("getIdentifier").invoke(dev)!!
            line("device $name id=${dev.id} vid=${dev.vendorId} pid=${dev.productId} full=${dev.keyboardType} descriptor=${dev.descriptor}")
            if (args.getString("action") == "set") {
                val layout = args.getString("layout")!!
                val set5 = method(im, "setKeyboardLayoutForInputDevice", 5)
                if (set5 != null) {
                    try {
                        set5.invoke(im, id, 0, imi, subtype, layout)
                        line("called setKeyboardLayoutForInputDevice(identifier, 0, ime, subtype, $layout)")
                    } catch (t: Throwable) {
                        line("setKeyboardLayoutForInputDevice failed ${describe(t)}")
                    }
                } else {
                    line("no setKeyboardLayoutForInputDevice with 5 parameters")
                }
                Thread.sleep(1500)
            }
            val get = method(im, "getKeyboardLayoutForInputDevice", 4)
            if (get != null) {
                try {
                    line("selected $name ${get.invoke(im, id, 0, imi, subtype)}")
                } catch (t: Throwable) {
                    line("selected $name ? ${describe(t)}")
                }
            }
            val list = method(im, "getKeyboardLayoutListForInputDevice", 4)
            if (list != null) {
                try {
                    val arr = list.invoke(im, id, 0, imi, subtype) as Array<*>
                    for (kl in arr) line("offered $name ${kl!!.javaClass.getMethod("getDescriptor").invoke(kl)}")
                } catch (t: Throwable) {
                    line("offered $name ? ${describe(t)}")
                }
            }
        }
    }
}
