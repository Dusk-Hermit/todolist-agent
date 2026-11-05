#include <jni.h>
#include <stdlib.h>
#include <string.h>
#include "_cgo_export.h"

static char *copy_bytes(JNIEnv *env, jbyteArray src) {
    jsize size = (*env)->GetArrayLength(env, src);
    char *out = malloc((size_t)size + 1);
    if (!out) return NULL;
    (*env)->GetByteArrayRegion(env, src, 0, size, (jbyte *)out);
    out[size] = '\0';
    return out;
}

static jbyteArray return_bytes(JNIEnv *env, char *src) {
    jsize size = (jsize)strlen(src);
    jbyteArray out = (*env)->NewByteArray(env, size);
    if (out) (*env)->SetByteArrayRegion(env, out, 0, size, (const jbyte *)src);
    free(src);
    return out;
}

JNIEXPORT jbyteArray JNICALL Java_dev_todolist_agent_MainActivity_nativeOpen(
        JNIEnv *env, jclass cls, jbyteArray dir) {
    char *path = copy_bytes(env, dir);
    if (!path) return NULL;
    jbyteArray result = return_bytes(env, TodoOpen(path));
    free(path);
    return result;
}

JNIEXPORT void JNICALL Java_dev_todolist_agent_MainActivity_nativeForeground(
        JNIEnv *env, jclass cls, jboolean foreground) {
    TodoForeground(foreground);
}

JNIEXPORT jbyteArray JNICALL Java_dev_todolist_agent_MainActivity_nativeDrain(
        JNIEnv *env, jclass cls) {
    return return_bytes(env, TodoDrain());
}

JNIEXPORT jbyteArray JNICALL Java_dev_todolist_agent_MainActivity_nativeInvoke(
        JNIEnv *env, jclass cls, jbyteArray method, jbyteArray data) {
    char *name = copy_bytes(env, method);
    char *args = copy_bytes(env, data);
    jbyteArray result = name && args ? return_bytes(env, TodoInvoke(name, args)) : NULL;
    free(name);
    free(args);
    return result;
}
