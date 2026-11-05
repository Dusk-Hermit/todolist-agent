package dev.todolist.agent;

import android.app.Activity;
import android.net.wifi.WifiManager;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.content.pm.ApplicationInfo;
import android.webkit.JavascriptInterface;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceRequest;
import android.webkit.WebView;
import android.webkit.WebViewClient;

import java.nio.charset.StandardCharsets;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public final class MainActivity extends Activity {
    private static final String APP_ORIGIN = "https://todo-agent.invalid/";
    private static final ExecutorService WORK = Executors.newSingleThreadExecutor();
    static { System.loadLibrary("todoagent"); }

    private static native byte[] nativeOpen(byte[] directory);
    private static native void nativeForeground(boolean foreground);
    private static native byte[] nativeDrain();
    private static native byte[] nativeInvoke(byte[] method, byte[] args);

    private final Handler ui = new Handler(Looper.getMainLooper());
    private WebView web;
    private WifiManager.MulticastLock multicast;
    private boolean resumed;
    private boolean loaded;

    private final Runnable poll = new Runnable() {
        @Override public void run() {
            if (!resumed || !loaded || web == null) return;
            WORK.execute(() -> {
                String script = fromBytes(nativeDrain());
                ui.post(() -> {
                    if (resumed && web != null && !script.isEmpty()) {
                        web.evaluateJavascript(script, null);
                    }
                    if (resumed) ui.postDelayed(this, 350);
                });
            });
        }
    };

    @Override public void onCreate(Bundle saved) {
        super.onCreate(saved);
        web = new WebView(this);
        web.getSettings().setJavaScriptEnabled(true);
        web.getSettings().setAllowFileAccess(false);
        web.getSettings().setAllowContentAccess(false);
        web.getSettings().setBlockNetworkLoads(true);
        if ((getApplicationInfo().flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0) {
            WebView.setWebContentsDebuggingEnabled(true);
        }
        web.setWebChromeClient(new WebChromeClient());
        web.addJavascriptInterface(new Bridge(), "TodoNative");
        web.setWebViewClient(new WebViewClient() {
            @Override public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                return true;
            }
            @Override public void onPageFinished(WebView view, String url) {
                loaded = true;
                if (resumed) {
                    ui.removeCallbacks(poll);
                    ui.post(poll);
                }
            }
        });
        setContentView(web);
        WORK.execute(() -> {
            String html = fromBytes(nativeOpen(bytes(getFilesDir().getAbsolutePath())));
            ui.post(() -> {
                if (web != null) web.loadDataWithBaseURL(APP_ORIGIN, html, "text/html", "UTF-8", null);
            });
        });
    }

    @Override public void onStart() {
        super.onStart();
        WifiManager wifi = (WifiManager) getApplicationContext().getSystemService(WIFI_SERVICE);
        if (wifi != null) {
            multicast = wifi.createMulticastLock("TodoList LAN discovery");
            multicast.setReferenceCounted(false);
            multicast.acquire();
        }
        resumed = true;
        WORK.execute(() -> nativeForeground(true));
        ui.post(poll);
    }

    @Override public void onStop() {
        resumed = false;
        ui.removeCallbacks(poll);
        WORK.execute(() -> nativeForeground(false));
        if (multicast != null && multicast.isHeld()) multicast.release();
        super.onStop();
    }

    @Override public void onDestroy() {
        if (web != null) {
            web.removeJavascriptInterface("TodoNative");
            web.destroy();
            web = null;
        }
        super.onDestroy();
    }

    private final class Bridge {
        @JavascriptInterface public void invoke(int id, String method, String args) {
            WORK.execute(() -> {
                String reply = fromBytes(nativeInvoke(bytes(method), bytes(args)));
                ui.post(() -> {
                    if (web != null) web.evaluateJavascript("window.androidReply(" + id + "," + reply + ")", null);
                });
            });
        }
    }

    private static byte[] bytes(String value) { return value.getBytes(StandardCharsets.UTF_8); }
    private static String fromBytes(byte[] value) { return new String(value, StandardCharsets.UTF_8); }

}
