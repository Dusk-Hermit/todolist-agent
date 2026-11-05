package dev.todolist.agent;

import android.app.Activity;
import android.app.AlertDialog;
import android.app.PendingIntent;
import android.content.ClipData;
import android.content.Intent;
import android.content.pm.PackageInstaller;
import android.content.pm.PackageInfo;
import android.net.Uri;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.provider.Settings;
import android.content.pm.ApplicationInfo;
import android.webkit.JavascriptInterface;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceRequest;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.nio.charset.StandardCharsets;
import java.io.File;
import java.io.FileInputStream;
import java.io.IOException;
import java.io.OutputStream;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public final class MainActivity extends Activity {
    private static final String APP_ORIGIN = "https://todo-agent.invalid/";
    private static final ExecutorService WORK = Executors.newSingleThreadExecutor();
    private static final ExecutorService INSTALL = Executors.newSingleThreadExecutor();
    static { System.loadLibrary("todoagent"); }

    private static native byte[] nativeOpen(byte[] directory);
    private static native void nativeForeground(boolean foreground);
    private static native byte[] nativeDrain();
    private static native byte[] nativeTakeUpdate();
    private static native void nativeUpdateError(byte[] message);
    private static native byte[] nativeInvoke(byte[] method, byte[] args);

    private final Handler ui = new Handler(Looper.getMainLooper());
    private WebView web;
    private WifiManager.MulticastLock multicast;
    private boolean resumed;
    private boolean loaded;
    private File pendingUpdate;
    private boolean waitingForInstallPermission;
    private AlertDialog shareDialog;
    private String sharedText;

    private final Runnable poll = new Runnable() {
        @Override public void run() {
            if (!resumed || !loaded || web == null) return;
            WORK.execute(() -> {
                String script = fromBytes(nativeDrain());
                String apk = fromBytes(nativeTakeUpdate());
                ui.post(() -> {
                    if (resumed && web != null && !script.isEmpty()) {
                        web.evaluateJavascript(script, null);
                    }
                    if (!apk.isEmpty()) pendingUpdate = new File(apk);
                    if (resumed && pendingUpdate != null && !waitingForInstallPermission) beginUpdate();
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
        if (saved == null) {
            receiveTextShare(getIntent());
        } else {
            confirmTextShare(saved.getString("sharedText"));
        }
    }

    @Override protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        receiveTextShare(intent);
    }

    @Override protected void onSaveInstanceState(Bundle state) {
        super.onSaveInstanceState(state);
        state.putString("sharedText", sharedText);
    }

    private void receiveTextShare(Intent intent) {
        if (intent == null || !"text/plain".equalsIgnoreCase(intent.getType())) return;
        try {
            CharSequence text;
            if (Intent.ACTION_PROCESS_TEXT.equals(intent.getAction())) {
                text = intent.getCharSequenceExtra(Intent.EXTRA_PROCESS_TEXT);
            } else if (Intent.ACTION_SEND.equals(intent.getAction())) {
                text = intent.getCharSequenceExtra(Intent.EXTRA_TEXT);
                ClipData clip = intent.getClipData();
                if (text == null && clip != null && clip.getItemCount() == 1) {
                    text = clip.getItemAt(0).getText();
                }
            } else {
                return;
            }
            if (text == null || text.toString().trim().isEmpty()) {
                showShareToast("没有可发送的文字");
            } else if (text.length() > 64000) {
                showShareToast("分享的文字过长，请分段发送");
            } else {
                confirmTextShare(text.toString());
            }
        } catch (RuntimeException e) {
            showShareToast("无法读取分享的文字");
        }
    }

    private void confirmTextShare(String text) {
        if (text == null) return;
        if (shareDialog != null) shareDialog.dismiss();
        sharedText = text;
        AlertDialog dialog = new AlertDialog.Builder(this)
                .setTitle("发送到 TodoList Agent")
                .setMessage(text)
                .setNegativeButton("取消", null)
                .setPositiveButton("加入待发队列", null)
                .create();
        shareDialog = dialog;
        dialog.setOnDismissListener(ignored -> {
            if (shareDialog == dialog) {
                shareDialog = null;
                sharedText = null;
            }
        });
        dialog.show();
        android.widget.Button submit = dialog.getButton(AlertDialog.BUTTON_POSITIVE);
        submit.setFilterTouchesWhenObscured(true);
        submit.setOnClickListener(ignored -> {
            submit.setEnabled(false);
            enqueueSharedText(text, dialog);
        });
    }

    private void enqueueSharedText(String text, AlertDialog dialog) {
        WORK.execute(() -> {
            String args = new JSONArray().put(text).toString();
            String reply = fromBytes(nativeInvoke(bytes("todo_sendInput"), bytes(args)));
            String error;
            try {
                error = new JSONObject(reply).optString("error");
            } catch (JSONException e) {
                error = "无法确认文字已保存";
            }
            String result = error;
            ui.post(() -> {
                if (isDestroyed()) return;
                if (result.isEmpty()) {
                    dialog.dismiss();
                    showShareToast("已加入待发队列");
                } else {
                    if (dialog.isShowing()) dialog.getButton(AlertDialog.BUTTON_POSITIVE).setEnabled(true);
                    showShareToast("无法加入队列：" + result);
                }
            });
        });
    }

    private void showShareToast(String text) {
        if (!isDestroyed()) Toast.makeText(this, text, Toast.LENGTH_SHORT).show();
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

    @Override public void onResume() {
        super.onResume();
        if (waitingForInstallPermission) {
            waitingForInstallPermission = false;
            if (!getPackageManager().canRequestPackageInstalls()) {
                pendingUpdate = null;
                reportUpdateError("请允许 TodoList Agent 安装更新，然后重试");
            }
        }
        if (pendingUpdate != null) beginUpdate();
    }

    private void beginUpdate() {
        if (!getPackageManager().canRequestPackageInstalls()) {
            waitingForInstallPermission = true;
            try {
                startActivity(new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                        Uri.parse("package:" + getPackageName())));
            } catch (RuntimeException e) {
                pendingUpdate = null;
                waitingForInstallPermission = false;
                reportUpdateError("无法打开 Android 安装权限设置");
            }
            return;
        }
        File apk = pendingUpdate;
        pendingUpdate = null;
        INSTALL.execute(() -> installAPK(apk));
    }

    private void installAPK(File apk) {
        PackageInstaller installer = getPackageManager().getPackageInstaller();
        int sessionId = -1;
        try {
            PackageInfo candidate = getPackageManager().getPackageArchiveInfo(apk.getAbsolutePath(), 0);
            PackageInfo current = getPackageManager().getPackageInfo(getPackageName(), 0);
            if (candidate == null || !getPackageName().equals(candidate.packageName) ||
                    versionCode(candidate) <= versionCode(current)) {
                throw new IOException("APK 不是此应用的更高版本");
            }
            PackageInstaller.SessionParams params =
                    new PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL);
            params.setAppPackageName(getPackageName());
            params.setSize(apk.length());
            if (Build.VERSION.SDK_INT >= 31) {
                params.setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED);
            }
            sessionId = installer.createSession(params);
            try (PackageInstaller.Session session = installer.openSession(sessionId);
                 FileInputStream in = new FileInputStream(apk);
                 OutputStream out = session.openWrite("base.apk", 0, apk.length())) {
                byte[] buffer = new byte[64 * 1024];
                for (int count; (count = in.read(buffer)) != -1;) out.write(buffer, 0, count);
                session.fsync(out);
            }
            int flags = PendingIntent.FLAG_UPDATE_CURRENT;
            if (Build.VERSION.SDK_INT >= 31) flags |= PendingIntent.FLAG_MUTABLE;
            Intent callback = new Intent(this, UpdateReceiver.class);
            PendingIntent result = PendingIntent.getBroadcast(this, sessionId, callback, flags);
            try (PackageInstaller.Session session = installer.openSession(sessionId)) {
                session.commit(result.getIntentSender());
            }
        } catch (IOException | android.content.pm.PackageManager.NameNotFoundException | RuntimeException e) {
            if (sessionId >= 0) {
                try { installer.abandonSession(sessionId); } catch (RuntimeException ignored) {}
            }
            reportUpdateError("Android 安装更新失败：" + e.getMessage());
        }
    }

    private static long versionCode(PackageInfo info) {
        return Build.VERSION.SDK_INT >= 28 ? info.getLongVersionCode() : info.versionCode;
    }

    static void reportUpdateError(String message) {
        WORK.execute(() -> nativeUpdateError(bytes(message)));
    }

    @Override public void onDestroy() {
        if (shareDialog != null) shareDialog.dismiss();
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
