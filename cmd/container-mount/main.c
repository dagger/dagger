#define _GNU_SOURCE
#include <fcntl.h>
#include <sched.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <unistd.h>
#include <errno.h>

static void usage(const char *prog) {
    fprintf(stderr, "Usage: %s <container-pid> <source-path> <target-path-in-container>\n", prog);
    fprintf(stderr, "\nExample:\n");
    fprintf(stderr, "  %s $(runc state mycontainer | jq -r .pid) /host/directory /container/mount/point\n", prog);
    exit(1);
}

int main(int argc, char *argv[]) {
    if (argc != 4) {
        usage(argv[0]);
    }

    pid_t container_pid = atoi(argv[1]);
    const char *source_path = argv[2];
    const char *target_path = argv[3];

    if (container_pid <= 0) {
        fprintf(stderr, "Error: Invalid container PID: %s\n", argv[1]);
        return 1;
    }

    // Step 1: Create a detached clone of the source mount
#ifdef DEBUG
    printf("Creating detached mount of %s...\n", source_path);
#endif
    int fd_mnt = open_tree(AT_FDCWD, source_path,
                          OPEN_TREE_CLONE | OPEN_TREE_CLOEXEC);
    if (fd_mnt < 0) {
        perror("open_tree failed");
        return 1;
    }
#ifdef DEBUG
    printf("Created detached mount, fd=%d\n", fd_mnt);
#endif

    // Step 2: Get file descriptors for the container's namespaces
    char path[256];

    // Mount namespace
    snprintf(path, sizeof(path), "/proc/%d/ns/mnt", container_pid);
    int fd_mntns = open(path, O_RDONLY);
    if (fd_mntns < 0) {
        perror("Failed to open mount namespace");
        close(fd_mnt);
        return 1;
    }

#ifdef DEBUG
    printf("Entering mount namespace of PID %d...\n", container_pid);
#endif
    if (setns(fd_mntns, CLONE_NEWNS) < 0) {
        perror("setns(CLONE_NEWNS) failed");
        close(fd_mntns);
        close(fd_mnt);
        return 1;
    }
    close(fd_mntns);

    // Step 3: Create target directory if it doesn't exist
    struct stat st;
    if (stat(target_path, &st) < 0) {
        if (errno == ENOENT) {
#ifdef DEBUG
            printf("Creating target directory %s...\n", target_path);
#endif
            if (mkdir(target_path, 0755) < 0 && errno != EEXIST) {
                perror("mkdir failed");
                // Continue anyway, might already exist
            }
        }
    }

    // Step 4: Unmount any existing mount at the target path
#ifdef DEBUG
    printf("Unmounting any existing mount at %s...\n", target_path);
#endif
    if (umount2(target_path, MNT_DETACH) < 0) {
        if (errno != EINVAL && errno != ENOENT) {
            perror("umount2 failed");
            // Continue anyway, might not be mounted
        }
    }

    // Step 5: Attach the detached mount to the target path in the container
#ifdef DEBUG
    printf("Attaching mount to %s in container...\n", target_path);
#endif
    if (move_mount(fd_mnt, "", AT_FDCWD, target_path, MOVE_MOUNT_F_EMPTY_PATH) < 0) {
        perror("move_mount failed");
        close(fd_mnt);
        return 1;
    }

    close(fd_mnt);
#ifdef DEBUG
    printf("Successfully mounted %s to %s in container PID %d\n",
           source_path, target_path, container_pid);
#endif

    return 0;
}