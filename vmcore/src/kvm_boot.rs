//! # KVM 미니 게스트 부팅 — x86 리얼 모드 Hello World
//!
//! ## 목적
//! 실제 `/dev/kvm` ioctl을 사용하여 "HCV"를 COM1 시리얼 포트(0x3F8)에
//! 출력하는 최소 x86 게스트를 부팅한다. KVM_RUN 루프와 I/O 포트
//! exit 처리의 완전한 데모.
//!
//! ## 스레드 안전성
//! KVM fd는 스레드 안전하나, 이 모듈의 함수들은 단일 스레드에서 사용을 전제한다.
//!
//! ## Architecture
//! ```text
//! Host (Rust)                Guest (x86 real mode)
//! ───────────                ─────────────────────
//! kvm_boot::boot_mini()      tiny_guest_code[]
//!   KvmSystem::open()          mov al, 'H'
//!   create_vm()                out 0x3F8, al  ← KVM_EXIT_IO
//!   set_memory_region()        mov al, 'C'
//!   create_vcpu()              out 0x3F8, al  ← KVM_EXIT_IO
//!   set_sregs (CS=0)           mov al, 'V'
//!   set_regs (RIP=0)           out 0x3F8, al  ← KVM_EXIT_IO
//!   loop { KVM_RUN }           hlt            ← KVM_EXIT_HLT
//! ```

use crate::kvm_sys::{KvmSysError, KvmSystem, KvmUserspaceMemoryRegion};

// KVM ioctl constants for vCPU register access
const KVM_GET_SREGS: libc::c_ulong = 0x8138AE83;
const KVM_SET_SREGS: libc::c_ulong = 0x4138AE84;
#[allow(dead_code)]
const KVM_GET_REGS: libc::c_ulong = 0x8090AE81;
const KVM_SET_REGS: libc::c_ulong = 0x4090AE82;
const KVM_RUN: libc::c_ulong = 0xAE80;

// KVM exit reasons
const KVM_EXIT_IO: u32 = 2;
const KVM_EXIT_HLT: u32 = 5;
const KVM_EXIT_SHUTDOWN: u32 = 8;

// I/O direction
const KVM_EXIT_IO_OUT: u8 = 1;

// COM1 serial port
const SERIAL_PORT: u16 = 0x3F8;

/// x86 guest code: writes "HCV\n" to COM1 (0x3F8) then halts
///
/// ```asm
/// mov al, 'H'     ; B0 48
/// out 0x3F8, al    ; E6 F8 (short form for port < 256... use long form)
/// ; Actually use: mov dx, 0x3F8; out dx, al
/// mov dx, 0x03F8   ; BA F8 03
/// mov al, 'H'      ; B0 48
/// out dx, al        ; EE
/// mov al, 'C'       ; B0 43
/// out dx, al        ; EE
/// mov al, 'V'       ; B0 56
/// out dx, al        ; EE
/// mov al, '\n'      ; B0 0A
/// out dx, al        ; EE
/// hlt               ; F4
/// ```
const GUEST_CODE: &[u8] = &[
    0xBA, 0xF8, 0x03, // mov dx, 0x03F8
    0xB0, 0x48, // mov al, 'H'
    0xEE, // out dx, al
    0xB0, 0x43, // mov al, 'C'
    0xEE, // out dx, al
    0xB0, 0x56, // mov al, 'V'
    0xEE, // out dx, al
    0xB0, 0x0A, // mov al, '\n'
    0xEE, // out dx, al
    0xF4, // hlt
];

/// KVM special registers (partial, for x86 real mode setup)
#[repr(C)]
#[derive(Debug, Default)]
struct KvmSregs {
    cs: KvmSegment,
    ds: KvmSegment,
    es: KvmSegment,
    fs: KvmSegment,
    gs: KvmSegment,
    ss: KvmSegment,
    tr: KvmSegment,
    ldt: KvmSegment,
    gdt: KvmDtable,
    idt: KvmDtable,
    cr0: u64,
    cr2: u64,
    cr3: u64,
    cr4: u64,
    cr8: u64,
    efer: u64,
    apic_base: u64,
    interrupt_bitmap: [u64; 4],
}

#[repr(C)]
#[derive(Debug, Default, Clone, Copy)]
struct KvmSegment {
    base: u64,
    limit: u32,
    selector: u16,
    type_: u8,
    present: u8,
    dpl: u8,
    db: u8,
    s: u8,
    l: u8,
    g: u8,
    avl: u8,
    _unusable: u8,
    _padding: u8,
}

#[repr(C)]
#[derive(Debug, Default, Clone, Copy)]
struct KvmDtable {
    base: u64,
    limit: u16,
    _padding: [u16; 3],
}

/// KVM general-purpose registers
#[repr(C)]
#[derive(Debug, Default)]
struct KvmRegs {
    rax: u64,
    rbx: u64,
    rcx: u64,
    rdx: u64,
    rsi: u64,
    rdi: u64,
    rsp: u64,
    rbp: u64,
    r8: u64,
    r9: u64,
    r10: u64,
    r11: u64,
    r12: u64,
    r13: u64,
    r14: u64,
    r15: u64,
    rip: u64,
    rflags: u64,
}

/// kvm_run structure (partial — only fields we need)
#[repr(C)]
struct KvmRun {
    request_interrupt_window: u8,
    immediate_exit: u8,
    _padding1: [u8; 6],
    exit_reason: u32,
    ready_for_interrupt_injection: u8,
    if_flag: u8,
    flags: u16,
    cr8: u64,
    apic_base: u64,
    // union starts here — we only handle IO exit
    _exit_union: [u8; 256], // large enough for any exit type
}

/// I/O exit info (within kvm_run union)
#[repr(C)]
#[derive(Debug)]
struct KvmRunExitIo {
    direction: u8,
    size: u8,
    port: u16,
    count: u32,
    data_offset: u64,
}

/// 부팅 결과
#[derive(Debug)]
pub struct BootResult {
    /// 시리얼 포트에서 캡처된 출력
    pub output: String,
    /// KVM 종료 사유 ("HLT", "SHUTDOWN", "MMIO" 등)
    pub exit_reason: &'static str,
    /// 실행된 KVM_RUN 호출 수
    pub instructions_executed: u32,
}

/// 시리얼 포트를 통해 "HCV\n"을 출력하는 최소 x86 게스트를 부팅한다.
///
/// 캡처된 시리얼 출력을 반환한다. `/dev/kvm`이 필요하다.
pub fn boot_mini_guest() -> Result<BootResult, KvmSysError> {
    let sys = KvmSystem::open()?;
    let vm = sys.create_vm()?;

    // Allocate 4KB guest memory
    let mem_size: usize = 0x1000;
    let guest_mem = unsafe {
        libc::mmap(
            std::ptr::null_mut(),
            mem_size,
            libc::PROT_READ | libc::PROT_WRITE,
            libc::MAP_PRIVATE | libc::MAP_ANONYMOUS,
            -1,
            0,
        )
    };
    if guest_mem == libc::MAP_FAILED {
        return Err(KvmSysError::MmapFailed(std::io::Error::last_os_error()));
    }

    // Copy guest code to address 0
    unsafe {
        std::ptr::copy_nonoverlapping(GUEST_CODE.as_ptr(), guest_mem as *mut u8, GUEST_CODE.len());
    }

    // Map guest memory
    let region = KvmUserspaceMemoryRegion {
        slot: 0,
        flags: 0,
        guest_phys_addr: 0,
        memory_size: mem_size as u64,
        userspace_addr: guest_mem as u64,
    };
    vm.set_user_memory_region(&region)?;

    // Create vCPU
    let mmap_size = sys.vcpu_mmap_size()?;
    let vcpu = vm.create_vcpu(0, mmap_size)?;

    // mmap the kvm_run structure
    let kvm_run_ptr = unsafe {
        libc::mmap(
            std::ptr::null_mut(),
            mmap_size,
            libc::PROT_READ | libc::PROT_WRITE,
            libc::MAP_SHARED,
            vcpu.as_raw_fd(),
            0,
        )
    };
    if kvm_run_ptr == libc::MAP_FAILED {
        unsafe { libc::munmap(guest_mem, mem_size) };
        return Err(KvmSysError::MmapFailed(std::io::Error::last_os_error()));
    }

    // Set up special registers: CS base = 0, for real mode at address 0
    let mut sregs = KvmSregs::default();
    let ret = unsafe { libc::ioctl(vcpu.as_raw_fd(), KVM_GET_SREGS, &mut sregs) };
    if ret < 0 {
        cleanup(guest_mem, mem_size, kvm_run_ptr, mmap_size);
        return Err(KvmSysError::Ioctl {
            op: "KVM_GET_SREGS",
            source: std::io::Error::last_os_error(),
        });
    }
    sregs.cs.base = 0;
    sregs.cs.selector = 0;
    let ret = unsafe { libc::ioctl(vcpu.as_raw_fd(), KVM_SET_SREGS, &sregs) };
    if ret < 0 {
        cleanup(guest_mem, mem_size, kvm_run_ptr, mmap_size);
        return Err(KvmSysError::Ioctl {
            op: "KVM_SET_SREGS",
            source: std::io::Error::last_os_error(),
        });
    }

    // Set general registers: RIP = 0, RFLAGS = 0x2 (mandatory bit)
    let regs = KvmRegs {
        rflags: 0x2,
        ..KvmRegs::default()
    };
    let ret = unsafe { libc::ioctl(vcpu.as_raw_fd(), KVM_SET_REGS, &regs) };
    if ret < 0 {
        cleanup(guest_mem, mem_size, kvm_run_ptr, mmap_size);
        return Err(KvmSysError::Ioctl {
            op: "KVM_SET_REGS",
            source: std::io::Error::last_os_error(),
        });
    }

    // Run the vCPU
    let mut output = String::new();
    let mut instructions = 0u32;
    let exit_reason;

    loop {
        let ret = unsafe { libc::ioctl(vcpu.as_raw_fd(), KVM_RUN, 0) };
        if ret < 0 {
            cleanup(guest_mem, mem_size, kvm_run_ptr, mmap_size);
            return Err(KvmSysError::Ioctl {
                op: "KVM_RUN",
                source: std::io::Error::last_os_error(),
            });
        }

        let kvm_run = unsafe { &*(kvm_run_ptr as *const KvmRun) };
        instructions += 1;

        match kvm_run.exit_reason {
            KVM_EXIT_IO => {
                let io = unsafe { &*(kvm_run._exit_union.as_ptr() as *const KvmRunExitIo) };
                if io.direction == KVM_EXIT_IO_OUT && io.port == SERIAL_PORT {
                    let data_ptr =
                        unsafe { (kvm_run_ptr as *const u8).add(io.data_offset as usize) };
                    let byte = unsafe { *data_ptr };
                    output.push(byte as char);
                }
            }
            KVM_EXIT_HLT => {
                exit_reason = "HLT";
                break;
            }
            KVM_EXIT_SHUTDOWN => {
                exit_reason = "SHUTDOWN";
                break;
            }
            other => {
                exit_reason = "UNKNOWN";
                tracing::warn!(exit_reason = other, "unexpected KVM exit");
                break;
            }
        }

        // Safety: prevent infinite loop
        if instructions > 100 {
            exit_reason = "MAX_INSTRUCTIONS";
            break;
        }
    }

    cleanup(guest_mem, mem_size, kvm_run_ptr, mmap_size);

    tracing::info!(
        output = %output.trim(),
        exit_reason,
        instructions,
        "mini guest completed"
    );

    Ok(BootResult {
        output,
        exit_reason,
        instructions_executed: instructions,
    })
}

fn cleanup(
    guest_mem: *mut libc::c_void,
    mem_size: usize,
    kvm_run: *mut libc::c_void,
    run_size: usize,
) {
    unsafe {
        libc::munmap(kvm_run, run_size);
        libc::munmap(guest_mem, mem_size);
    }
}

// KVM exit reason for MMIO
const KVM_EXIT_MMIO: u32 = 6;

/// bzImage 파일에서 Linux 커널을 부팅한다.
/// COM1 시리얼 출력(포트 0x3F8)을 최대 `max_output` 바이트까지 캡처한다.
/// 캡처된 출력과 종료 사유를 반환한다.
pub fn boot_linux(
    bzimage_path: &str,
    cmdline: &str,
    memory_mb: u64,
    max_output: usize,
    max_instructions: u64,
) -> Result<BootResult, KvmSysError> {
    let sys = KvmSystem::open()?;
    let vm = sys.create_vm()?;

    let mem_size = (memory_mb as usize) * 1024 * 1024;

    // mmap guest memory (page-aligned)
    let guest_mem = unsafe {
        libc::mmap(
            std::ptr::null_mut(),
            mem_size,
            libc::PROT_READ | libc::PROT_WRITE,
            libc::MAP_PRIVATE | libc::MAP_ANONYMOUS,
            -1,
            0,
        )
    };
    if guest_mem == libc::MAP_FAILED {
        return Err(KvmSysError::MmapFailed(std::io::Error::last_os_error()));
    }

    // Map guest memory to VM
    let region = KvmUserspaceMemoryRegion {
        slot: 0,
        flags: 0,
        guest_phys_addr: 0,
        memory_size: mem_size as u64,
        userspace_addr: guest_mem as u64,
    };
    vm.set_user_memory_region(&region)?;

    // Load bzImage using kvm_loader
    let entry =
        unsafe { crate::kvm_loader::load_bzimage(guest_mem as *mut u8, mem_size, bzimage_path) }
            .map_err(|e| KvmSysError::Ioctl {
                op: "load_bzimage",
                source: std::io::Error::other(e.to_string()),
            })?;

    // Set command line if provided
    if !cmdline.is_empty() {
        unsafe {
            crate::kvm_loader::set_cmdline(guest_mem as *mut u8, cmdline);
        }
    }

    // Create vCPU
    let mmap_size = sys.vcpu_mmap_size()?;
    let vcpu = vm.create_vcpu(0, mmap_size)?;

    // mmap the kvm_run structure
    let kvm_run_ptr = unsafe {
        libc::mmap(
            std::ptr::null_mut(),
            mmap_size,
            libc::PROT_READ | libc::PROT_WRITE,
            libc::MAP_SHARED,
            vcpu.as_raw_fd(),
            0,
        )
    };
    if kvm_run_ptr == libc::MAP_FAILED {
        unsafe {
            libc::munmap(guest_mem, mem_size);
        }
        return Err(KvmSysError::MmapFailed(std::io::Error::last_os_error()));
    }

    // Set up special registers for 32-bit protected mode
    let mut sregs = KvmSregs::default();
    let ret = unsafe { libc::ioctl(vcpu.as_raw_fd(), KVM_GET_SREGS, &mut sregs) };
    if ret < 0 {
        cleanup(guest_mem, mem_size, kvm_run_ptr, mmap_size);
        return Err(KvmSysError::Ioctl {
            op: "KVM_GET_SREGS",
            source: std::io::Error::last_os_error(),
        });
    }

    // CR0: enable Protected Mode
    sregs.cr0 |= 0x1; // PE bit

    // CS: 32-bit code segment
    sregs.cs.base = 0;
    sregs.cs.limit = 0xFFFFFFFF;
    sregs.cs.selector = 0x10;
    sregs.cs.type_ = 0xB; // execute/read, accessed
    sregs.cs.present = 1;
    sregs.cs.dpl = 0;
    sregs.cs.db = 1; // 32-bit
    sregs.cs.s = 1; // code/data segment
    sregs.cs.l = 0;
    sregs.cs.g = 1; // 4KB granularity

    // DS/ES/SS: data segments
    let data_seg = KvmSegment {
        base: 0,
        limit: 0xFFFFFFFF,
        selector: 0x18,
        type_: 0x3, // read/write, accessed
        present: 1,
        dpl: 0,
        db: 1,
        s: 1,
        l: 0,
        g: 1,
        avl: 0,
        _unusable: 0,
        _padding: 0,
    };
    sregs.ds = data_seg;
    sregs.es = data_seg;
    sregs.ss = data_seg;

    let ret = unsafe { libc::ioctl(vcpu.as_raw_fd(), KVM_SET_SREGS, &sregs) };
    if ret < 0 {
        cleanup(guest_mem, mem_size, kvm_run_ptr, mmap_size);
        return Err(KvmSysError::Ioctl {
            op: "KVM_SET_SREGS",
            source: std::io::Error::last_os_error(),
        });
    }

    // Set general registers: RIP = entry point, RSI = boot_params address
    let regs = KvmRegs {
        rip: entry,
        rsi: 0x7000, // BOOT_PARAMS_ADDR
        rflags: 0x2,
        ..KvmRegs::default()
    };
    let ret = unsafe { libc::ioctl(vcpu.as_raw_fd(), KVM_SET_REGS, &regs) };
    if ret < 0 {
        cleanup(guest_mem, mem_size, kvm_run_ptr, mmap_size);
        return Err(KvmSysError::Ioctl {
            op: "KVM_SET_REGS",
            source: std::io::Error::last_os_error(),
        });
    }

    // KVM_RUN loop with serial capture
    let mut output = String::new();
    let mut instructions = 0u64;
    let exit_reason;

    loop {
        let ret = unsafe { libc::ioctl(vcpu.as_raw_fd(), KVM_RUN, 0) };
        if ret < 0 {
            cleanup(guest_mem, mem_size, kvm_run_ptr, mmap_size);
            return Err(KvmSysError::Ioctl {
                op: "KVM_RUN",
                source: std::io::Error::last_os_error(),
            });
        }

        let kvm_run = unsafe { &*(kvm_run_ptr as *const KvmRun) };
        instructions += 1;

        match kvm_run.exit_reason {
            KVM_EXIT_IO => {
                let io = unsafe { &*(kvm_run._exit_union.as_ptr() as *const KvmRunExitIo) };
                if io.direction == KVM_EXIT_IO_OUT && io.port == SERIAL_PORT {
                    let data_ptr =
                        unsafe { (kvm_run_ptr as *const u8).add(io.data_offset as usize) };
                    let byte = unsafe { *data_ptr };
                    if output.len() < max_output {
                        output.push(byte as char);
                    }
                }
            }
            KVM_EXIT_HLT => {
                exit_reason = "HLT";
                break;
            }
            KVM_EXIT_SHUTDOWN => {
                exit_reason = "SHUTDOWN";
                break;
            }
            KVM_EXIT_MMIO => {
                // Ignore MMIO exits (no device backing)
                exit_reason = "MMIO";
                break;
            }
            other => {
                exit_reason = "UNKNOWN";
                tracing::warn!(exit_reason = other, "unexpected KVM exit");
                break;
            }
        }

        if instructions >= max_instructions {
            exit_reason = "MAX_INSTRUCTIONS";
            break;
        }
    }

    cleanup(guest_mem, mem_size, kvm_run_ptr, mmap_size);

    tracing::info!(
        output_len = output.len(),
        exit_reason,
        instructions,
        "Linux guest completed"
    );

    Ok(BootResult {
        output,
        exit_reason,
        instructions_executed: instructions as u32,
    })
}

// ═══════════════════════════════════════════════════════════
// FFI
// ═══════════════════════════════════════════════════════════

// FFI: Go에서 호출. 미니 게스트를 부팅한다. 반환값: 출력 바이트 수(>=0) 또는 음수 에러.
// 출력은 `out_buf`에 null 종료 문자열로 기록된다.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_kvm_boot_mini(out_buf: *mut u8, buf_len: u32) -> i32 {
    crate::panic_barrier::catch(|| {
        if out_buf.is_null() || buf_len == 0 {
            return crate::panic_barrier::ErrorCode::InvalidArg as i32;
        }
        match boot_mini_guest() {
            Ok(result) => {
                let bytes = result.output.as_bytes();
                let copy_len = std::cmp::min(bytes.len(), (buf_len - 1) as usize);
                unsafe {
                    std::ptr::copy_nonoverlapping(bytes.as_ptr(), out_buf, copy_len);
                    *out_buf.add(copy_len) = 0; // null terminate
                }
                copy_len as i32
            }
            Err(e) => {
                tracing::error!(%e, "mini guest boot failed");
                crate::panic_barrier::ErrorCode::KvmError as i32
            }
        }
    })
}

// FFI: Go에서 호출. bzImage 파일에서 Linux 커널을 부팅한다.
// `bzimage_path`와 `cmdline`은 null 종료 C 문자열.
// 반환값: 출력 바이트 수(>=0) 또는 음수 에러 코드.
#[no_mangle]
#[allow(clippy::not_unsafe_ptr_arg_deref)]
pub extern "C" fn hcv_kvm_boot_linux(
    bzimage_path: *const libc::c_char,
    cmdline: *const libc::c_char,
    memory_mb: u64,
    out_buf: *mut u8,
    buf_len: u32,
) -> i32 {
    crate::panic_barrier::catch(|| {
        if bzimage_path.is_null() || out_buf.is_null() || buf_len == 0 {
            return crate::panic_barrier::ErrorCode::InvalidArg as i32;
        }

        let path = unsafe { std::ffi::CStr::from_ptr(bzimage_path) };
        let path_str = match path.to_str() {
            Ok(s) => s,
            Err(_) => return crate::panic_barrier::ErrorCode::InvalidArg as i32,
        };

        let cmd = if cmdline.is_null() {
            ""
        } else {
            let cstr = unsafe { std::ffi::CStr::from_ptr(cmdline) };
            match cstr.to_str() {
                Ok(s) => s,
                Err(_) => return crate::panic_barrier::ErrorCode::InvalidArg as i32,
            }
        };

        match boot_linux(path_str, cmd, memory_mb, (buf_len - 1) as usize, 1_000_000) {
            Ok(result) => {
                let bytes = result.output.as_bytes();
                let copy_len = std::cmp::min(bytes.len(), (buf_len - 1) as usize);
                unsafe {
                    std::ptr::copy_nonoverlapping(bytes.as_ptr(), out_buf, copy_len);
                    *out_buf.add(copy_len) = 0; // null terminate
                }
                copy_len as i32
            }
            Err(e) => {
                tracing::error!(%e, "Linux boot failed");
                crate::panic_barrier::ErrorCode::KvmError as i32
            }
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_boot_linux_missing_image() {
        match boot_linux("/nonexistent/bzImage", "console=ttyS0", 64, 4096, 1000) {
            Ok(_) => panic!("expected error for missing bzImage"),
            Err(KvmSysError::OpenFailed(_)) => {
                eprintln!("SKIP: /dev/kvm not available");
            }
            Err(KvmSysError::Ioctl { op, .. }) => {
                assert_eq!(op, "load_bzimage", "expected load_bzimage error");
            }
            Err(e) => {
                // Any other KVM error is acceptable (e.g., no /dev/kvm)
                eprintln!("Got expected error: {e}");
            }
        }
    }

    #[test]
    fn test_boot_mini_guest() {
        match boot_mini_guest() {
            Ok(result) => {
                println!("Guest output: {:?}", result.output);
                println!("Exit reason: {}", result.exit_reason);
                println!("Instructions: {}", result.instructions_executed);
                assert_eq!(result.output.trim(), "HCV");
                assert_eq!(result.exit_reason, "HLT");
                assert!(result.instructions_executed > 0);
            }
            Err(KvmSysError::OpenFailed(_)) => {
                eprintln!("SKIP: /dev/kvm not available");
            }
            Err(e) => panic!("boot failed: {e}"),
        }
    }

    #[test]
    fn test_guest_code_bytes() {
        // Verify guest code is valid: should end with HLT (0xF4)
        assert_eq!(*GUEST_CODE.last().unwrap(), 0xF4);
        // Should contain 4 OUT instructions (0xEE)
        let out_count = GUEST_CODE.iter().filter(|&&b| b == 0xEE).count();
        assert_eq!(out_count, 4); // H, C, V, \n
    }

    #[test]
    fn test_ffi_boot() {
        let mut buf = vec![0u8; 64];
        let result = hcv_kvm_boot_mini(buf.as_mut_ptr(), 64);
        if result >= 0 {
            let output = std::str::from_utf8(&buf[..result as usize]).unwrap();
            println!("FFI output: {:?}", output);
            assert!(output.contains("HCV"));
        } else if result == crate::panic_barrier::ErrorCode::KvmError as i32 {
            eprintln!("SKIP: KVM not available via FFI");
        }
    }
}
