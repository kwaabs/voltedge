package com.voltedge.voltedge_field

import android.Manifest
import android.annotation.SuppressLint
import android.bluetooth.BluetoothAdapter
import android.bluetooth.BluetoothManager
import android.bluetooth.BluetoothSocket
import android.content.Context
import android.content.pm.PackageManager
import android.location.LocationManager
import android.location.OnNmeaMessageListener
import android.os.Build
import android.os.Handler
import android.os.Looper
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.EventChannel
import io.flutter.plugin.common.MethodChannel
import java.util.UUID

class MainActivity : FlutterActivity() {
    private val mainHandler = Handler(Looper.getMainLooper())
    private var btBridge: BluetoothNmeaBridge? = null

    // A requestPermission() MethodChannel call parked while the OS permission
    // dialog is up; onRequestPermissionsResult delivers its granted/denied answer.
    private var pendingPermissionResult: MethodChannel.Result? = null

    // Force MapLibre Native to treat itself as connected. It gates ALL HTTP
    // requests on OS connectivity, and our offline pack is served over a local
    // loopback HTTP tile server -- so with no network MapLibre refuses to fetch
    // even 127.0.0.1 and the map stays blank. This app is offline by design, so
    // we always want requests to go through. Dart calls this once the map is
    // created (after maplibre_gl has initialized the SDK), so the override sticks.
    //
    // Reflection, not a direct import: org.maplibre.android.MapLibre lives in the
    // maplibre_gl plugin's own Gradle module and isn't on THIS app module's
    // compile classpath, but it IS on the runtime classpath by the time the map
    // exists. Reflection also survives its one-arg setConnected being either a
    // boxed Boolean or a primitive boolean across SDK versions.
    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)

        // Raw NMEA -> Dart, for the GNSS quality engine (Phase: gnss-quality). The
        // OS location APIs give lat/lon/accuracy but drop DOP/satellite/fix-type;
        // those only exist in the receiver's raw NMEA. We surface it via an
        // EventChannel that registers an OnNmeaMessageListener while Dart is
        // listening; the Dart-side NmeaParser turns the sentence stream into DOP.
        // Sentences only flow while location updates are active, which geolocator
        // starts in parallel from Dart, so nothing extra powers the GPS here.
        EventChannel(flutterEngine.dartExecutor.binaryMessenger, NMEA_CHANNEL)
            .setStreamHandler(NmeaStreamHandler(applicationContext))

        // External Bluetooth GNSS (Phase: gnss-quality, Slice 4). The bridge owns
        // the RFCOMM socket + read thread; the same object backs BOTH the control
        // MethodChannel (list bonded devices / connect / disconnect) and the
        // bt_nmea EventChannel (the socket's NMEA line stream). Same shape as the
        // internal NMEA channel, so the Dart-side NmeaParser is reused verbatim --
        // an external survey receiver's NMEA carries lat/lon too (unlike internal,
        // where geolocator is authoritative), so the Dart source reads position
        // from the parser. iOS has no Classic-SPP path, so this is Android-only.
        val bridge = BluetoothNmeaBridge(applicationContext, mainHandler)
        btBridge = bridge
        EventChannel(flutterEngine.dartExecutor.binaryMessenger, BT_NMEA_CHANNEL)
            .setStreamHandler(bridge)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, BT_CHANNEL)
            .setMethodCallHandler { call, result -> handleBt(call.method, call, bridge, result) }

        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, CHANNEL)
            .setMethodCallHandler { call, result ->
                when (call.method) {
                    "setConnected" -> {
                        try {
                            val cls = Class.forName("org.maplibre.android.MapLibre")
                            // The plugin usually initializes the SDK already; call
                            // getInstance defensively and ignore if unavailable.
                            try {
                                cls.getMethod("getInstance", android.content.Context::class.java)
                                    .invoke(null, applicationContext)
                            } catch (_: Throwable) {
                            }
                            val setter = cls.methods.firstOrNull {
                                it.name == "setConnected" && it.parameterTypes.size == 1
                            } ?: throw NoSuchMethodException("MapLibre.setConnected")
                            setter.invoke(null, true)
                            result.success(true)
                        } catch (e: Throwable) {
                            result.error("maplibre", e.message, null)
                        }
                    }
                    else -> result.notImplemented()
                }
            }
    }

    private fun handleBt(
        method: String,
        call: io.flutter.plugin.common.MethodCall,
        bridge: BluetoothNmeaBridge,
        result: MethodChannel.Result,
    ) {
        when (method) {
            "isSupported" -> result.success(bridge.isSupported())
            "isEnabled" -> result.success(bridge.isEnabled())
            "hasPermission" -> result.success(hasBtConnectPermission())
            "requestPermission" -> requestBtConnectPermission(result)
            "bondedDevices" -> {
                if (!hasBtConnectPermission()) {
                    result.error("bt_permission", "Bluetooth permission not granted", null)
                } else {
                    result.success(bridge.bondedDevices())
                }
            }
            "connect" -> {
                val address = call.argument<String>("address")
                if (address.isNullOrEmpty()) {
                    result.error("bad_args", "address is required", null)
                    return
                }
                if (!hasBtConnectPermission()) {
                    result.error("bt_permission", "Bluetooth permission not granted", null)
                    return
                }
                // connect() blocks on the RFCOMM handshake, so run it off the main
                // thread and post the outcome back.
                Thread {
                    try {
                        bridge.connect(address)
                        mainHandler.post { result.success(true) }
                    } catch (e: Throwable) {
                        mainHandler.post { result.error("bt_connect", e.message, null) }
                    }
                }.start()
            }
            "disconnect" -> {
                bridge.disconnect()
                result.success(true)
            }
            else -> result.notImplemented()
        }
    }

    private fun hasBtConnectPermission(): Boolean {
        // BLUETOOTH_CONNECT is an Android 12+ (API 31) runtime permission. Before
        // that the legacy BLUETOOTH permission is normal (granted at install), so
        // connecting to an already-bonded device needs no runtime grant.
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) return true
        return ContextCompat.checkSelfPermission(this, Manifest.permission.BLUETOOTH_CONNECT) ==
            PackageManager.PERMISSION_GRANTED
    }

    private fun requestBtConnectPermission(result: MethodChannel.Result) {
        if (hasBtConnectPermission()) {
            result.success(true)
            return
        }
        // Only one dialog at a time; a second request while one is pending is
        // rejected rather than clobbering the parked result.
        if (pendingPermissionResult != null) {
            result.error("bt_permission_busy", "A permission request is already in progress", null)
            return
        }
        pendingPermissionResult = result
        ActivityCompat.requestPermissions(
            this,
            arrayOf(Manifest.permission.BLUETOOTH_CONNECT),
            BT_PERM_REQUEST,
        )
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray,
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode == BT_PERM_REQUEST) {
            val granted = grantResults.isNotEmpty() &&
                grantResults[0] == PackageManager.PERMISSION_GRANTED
            pendingPermissionResult?.success(granted)
            pendingPermissionResult = null
        }
    }

    override fun onDestroy() {
        btBridge?.disconnect()
        super.onDestroy()
    }

    companion object {
        private const val CHANNEL = "voltedge/field/maplibre"
        private const val NMEA_CHANNEL = "voltedge/field/nmea"
        private const val BT_CHANNEL = "voltedge/field/bt"
        private const val BT_NMEA_CHANNEL = "voltedge/field/bt_nmea"
        private const val BT_PERM_REQUEST = 5309
    }
}

/// Streams raw NMEA sentences to Dart while there is a listener. It attaches an
/// OnNmeaMessageListener (API 24+) to the system LocationManager and forwards each
/// sentence verbatim; the Dart NmeaParser validates checksums and extracts DOP.
/// On API < 24 (no NMEA callback) it simply produces nothing -- the Dart side
/// treats an empty stream as "no DOP", exactly like iOS. Registration needs
/// ACCESS_FINE_LOCATION, which Dart requests before starting; a missing grant
/// surfaces as a stream error rather than a crash.
private class NmeaStreamHandler(private val context: Context) :
    EventChannel.StreamHandler {
    private val handler = Handler(Looper.getMainLooper())
    private var listener: OnNmeaMessageListener? = null

    override fun onListen(arguments: Any?, events: EventChannel.EventSink) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.N) return
        val lm = context.getSystemService(Context.LOCATION_SERVICE) as? LocationManager
            ?: return
        val l = OnNmeaMessageListener { message, _ -> events.success(message) }
        try {
            lm.addNmeaListener(l, handler)
            listener = l
        } catch (e: SecurityException) {
            events.error("nmea_permission", e.message, null)
        }
    }

    override fun onCancel(arguments: Any?) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.N) return
        val lm = context.getSystemService(Context.LOCATION_SERVICE) as? LocationManager
        listener?.let { lm?.removeNmeaListener(it) }
        listener = null
    }
}

/// Bridges an external Bluetooth GNSS receiver's NMEA stream to Dart (Phase:
/// gnss-quality, Slice 4). One object backs both the control MethodChannel and the
/// bt_nmea EventChannel. It opens a Classic RFCOMM (SPP) socket to an
/// already-bonded receiver, reads its byte stream on a background thread, splits it
/// into newline-delimited NMEA sentences, and forwards each to the current
/// EventSink on the main thread. Pairing itself is done in the OS Bluetooth
/// settings -- we only ever connect to bonded devices, so no discovery/scan (and
/// no scan permission) is needed. All BluetoothAdapter/socket calls need
/// BLUETOOTH_CONNECT on API 31+, which the Activity gates before calling in.
///
/// Ordering contract: the Dart source subscribes to the EventChannel (sets [sink])
/// BEFORE it calls connect(), so no sentence is dropped for want of a sink.
@SuppressLint("MissingPermission")
private class BluetoothNmeaBridge(
    private val context: Context,
    private val mainHandler: Handler,
) : EventChannel.StreamHandler {
    private var sink: EventChannel.EventSink? = null
    private var socket: BluetoothSocket? = null
    private var readThread: Thread? = null

    @Volatile
    private var running = false

    // The well-known Serial Port Profile UUID every NMEA receiver advertises.
    private val sppUuid = UUID.fromString("00001101-0000-1000-8000-00805F9B34FB")

    private val adapter: BluetoothAdapter?
        get() = (context.getSystemService(Context.BLUETOOTH_SERVICE) as? BluetoothManager)?.adapter

    override fun onListen(arguments: Any?, events: EventChannel.EventSink) {
        sink = events
    }

    override fun onCancel(arguments: Any?) {
        sink = null
    }

    fun isSupported(): Boolean = adapter != null

    fun isEnabled(): Boolean = adapter?.isEnabled == true

    fun bondedDevices(): List<Map<String, String>> {
        val a = adapter ?: return emptyList()
        return try {
            a.bondedDevices?.map {
                mapOf("name" to (it.name ?: it.address), "address" to it.address)
            } ?: emptyList()
        } catch (e: SecurityException) {
            emptyList()
        }
    }

    fun connect(address: String) {
        disconnect()
        val a = adapter ?: throw IllegalStateException("Bluetooth is not available on this device")
        if (!a.isEnabled) throw IllegalStateException("Bluetooth is turned off")
        val device = a.getRemoteDevice(address)
        val s = device.createRfcommSocketToServiceRecord(sppUuid)
        // Discovery, if running, slows/breaks an RFCOMM connect -- cancel it first.
        try {
            a.cancelDiscovery()
        } catch (_: Throwable) {
        }
        s.connect() // blocks until connected or throws
        socket = s
        running = true
        val t = Thread { readLoop(s) }
        t.isDaemon = true
        readThread = t
        t.start()
    }

    private fun readLoop(s: BluetoothSocket) {
        val buffer = StringBuilder()
        val chunk = ByteArray(1024)
        try {
            val input = s.inputStream
            while (running) {
                val n = input.read(chunk)
                if (n < 0) break // stream closed
                buffer.append(String(chunk, 0, n, Charsets.US_ASCII))
                var nl = buffer.indexOf("\n")
                while (nl >= 0) {
                    val line = buffer.substring(0, nl).trim()
                    buffer.delete(0, nl + 1)
                    if (line.isNotEmpty()) {
                        mainHandler.post { sink?.success(line) }
                    }
                    nl = buffer.indexOf("\n")
                }
                // Guard against a peer that never sends a newline: don't let the
                // buffer grow without bound.
                if (buffer.length > 8192) buffer.setLength(0)
            }
        } catch (e: Throwable) {
            if (running) mainHandler.post { sink?.error("bt_read", e.message, null) }
        }
    }

    fun disconnect() {
        running = false
        try {
            socket?.close()
        } catch (_: Throwable) {
        }
        socket = null
        readThread = null
    }
}
