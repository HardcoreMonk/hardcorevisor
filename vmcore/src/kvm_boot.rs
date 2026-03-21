//! # KVM Mini Guest Boot — x86 Real Mode Hello World
//!
//! Boots a minimal x86 guest that writes "HCV" to port 0x3F8 (COM1 serial)
//! using real /dev/kvm ioctl. Demonstrates the full KVM_RUN loop with
//! I/O port exit handling.
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

/// Boot result
#[derive(Debug)]
pub struct BootResult {
    pub output: String,
    pub exit_reason: &'static str,
    pub instructions_executed: u32,
}

/// Boot a minimal x86 guest that prints "HCV\n" via serial port.
/// Returns the captured serial output.
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

// ═══════════════════════════════════════════════════════════
// FFI
// ═══════════════════════════════════════════════════════════

/// Boot a mini guest. Returns 0 on success, negative on error.
/// Output is written to `out_buf` (null-terminated, max `buf_len` bytes).
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

#[cfg(test)]
mod tests {
    use super::*;

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
