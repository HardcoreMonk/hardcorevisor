//! # Linux Kernel Boot — bzImage Loader
//!
//! Loads a Linux bzImage into guest memory following the x86 boot protocol.
//! Sets up boot_params, e820 memory map, and command line for kernel boot.

// ── Error type ───────────────────────────────────────────

/// Errors from bzImage loading operations
#[derive(Debug)]
pub enum LoaderError {
    /// I/O error reading the bzImage file
    Io(std::io::Error),
    /// Invalid bzImage format
    InvalidFormat(&'static str),
    /// Image is too large for guest memory
    TooLarge { image_size: usize, mem_size: usize },
}

impl std::fmt::Display for LoaderError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            LoaderError::Io(e) => write!(f, "I/O error: {e}"),
            LoaderError::InvalidFormat(msg) => write!(f, "invalid bzImage: {msg}"),
            LoaderError::TooLarge {
                image_size,
                mem_size,
            } => {
                write!(
                    f,
                    "kernel too large: {image_size} bytes, guest memory {mem_size} bytes"
                )
            }
        }
    }
}

impl std::error::Error for LoaderError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            LoaderError::Io(e) => Some(e),
            _ => None,
        }
    }
}

impl From<std::io::Error> for LoaderError {
    fn from(e: std::io::Error) -> Self {
        LoaderError::Io(e)
    }
}

// ── Boot protocol structures ─────────────────────────────

/// E820 memory map entry type: usable RAM
pub const E820_RAM: u32 = 1;
/// E820 memory map entry type: reserved
pub const E820_RESERVED: u32 = 2;

/// E820 memory map entry
#[repr(C, packed)]
#[derive(Debug, Default, Clone, Copy)]
pub struct E820Entry {
    pub addr: u64,
    pub size: u64,
    pub entry_type: u32,
}

/// Linux setup header (partial — fields needed for basic boot)
///
/// Located at offset 0x1F1 in the bzImage / boot_params.
#[repr(C)]
#[derive(Debug, Default, Clone, Copy)]
pub struct SetupHeader {
    pub setup_sects: u8,    // 0x1F1
    pub _pad: [u8; 15],     // 0x1F2 .. 0x200
    pub type_of_loader: u8, // 0x210
    pub loadflags: u8,      // 0x211
    pub _pad2: [u8; 10],    // 0x212 .. 0x21B
    pub cmd_line_ptr: u32,  // 0x228
    pub _pad3: [u8; 16],    // 0x22C .. 0x23B
}

/// Size of SetupHeader in bytes
const SETUP_HEADER_SIZE: usize = std::mem::size_of::<SetupHeader>();

/// Linux boot_params structure (partial — enough for basic boot).
///
/// Full structure is 4096 bytes; we define the fields we need and pad the rest.
#[repr(C)]
pub struct BootParams {
    pub screen_info: [u8; 64],                         // 0x000
    pub _pad1: [u8; 16],                               // 0x040
    pub ext_ramdisk_image: u32,                        // 0x050 (unused)
    pub _pad2: [u8; 404],                              // 0x054 .. 0x1E7
    pub e820_entries: u8,                              // 0x1E8
    pub _pad3: [u8; 7],                                // 0x1E9 .. 0x1EF
    pub hdr: SetupHeader,                              // 0x1F0
    pub _pad4: [u8; 2048 - 0x1F0 - SETUP_HEADER_SIZE], // fill to 0x800
    pub e820_table: [E820Entry; 128],                  // 0x800 ..
}

// ── Address constants ────────────────────────────────────

/// Address where boot_params are placed in guest memory
const BOOT_PARAMS_ADDR: usize = 0x7000;

/// Address where the kernel command line is placed
const CMDLINE_ADDR: usize = 0x20000;

/// Address where the protected-mode kernel is loaded (1 MB)
const KERNEL_LOAD_ADDR: usize = 0x100000;

/// Default kernel command line
const DEFAULT_CMDLINE: &[u8] = b"console=ttyS0 noapic\0";

// ── Loader ───────────────────────────────────────────────

/// Load a bzImage into guest memory.
/// Returns the entry point address (typically 0x100000).
///
/// # Safety
/// `guest_mem` must point to a valid, writable memory region of at least `mem_size` bytes.
pub unsafe fn load_bzimage(
    guest_mem: *mut u8,
    mem_size: usize,
    bzimage_path: &str,
) -> Result<u64, LoaderError> {
    let data = std::fs::read(bzimage_path)?;

    // Verify minimum size and magic "HdrS" at offset 0x202
    if data.len() < 0x206 {
        return Err(LoaderError::InvalidFormat("file too small for bzImage"));
    }
    if &data[0x202..0x206] != b"HdrS" {
        return Err(LoaderError::InvalidFormat("missing HdrS magic at 0x202"));
    }

    // Read setup_sects from offset 0x1F1
    let setup_sects = if data[0x1F1] == 0 {
        4
    } else {
        data[0x1F1] as usize
    };
    let setup_size = (setup_sects + 1) * 512;
    let kernel_offset = setup_size;

    // Validate sizes
    if kernel_offset >= data.len() {
        return Err(LoaderError::InvalidFormat("setup_sects exceeds file size"));
    }

    let kernel_size = data.len() - kernel_offset;
    let kernel_end = KERNEL_LOAD_ADDR + kernel_size;
    if kernel_end > mem_size {
        return Err(LoaderError::TooLarge {
            image_size: kernel_end,
            mem_size,
        });
    }

    // Copy protected-mode kernel to KERNEL_LOAD_ADDR (1 MB)
    std::ptr::copy_nonoverlapping(
        data[kernel_offset..].as_ptr(),
        guest_mem.add(KERNEL_LOAD_ADDR),
        kernel_size,
    );

    // Set up boot_params at BOOT_PARAMS_ADDR
    let boot_params_ptr = guest_mem.add(BOOT_PARAMS_ADDR) as *mut BootParams;
    std::ptr::write_bytes(boot_params_ptr, 0, 1);

    let bp = &mut *boot_params_ptr;

    // Copy the original setup header from the bzImage
    let hdr_src_offset = 0x1F1;
    let hdr_copy_len = std::cmp::min(SETUP_HEADER_SIZE, data.len() - hdr_src_offset);
    std::ptr::copy_nonoverlapping(
        data[hdr_src_offset..].as_ptr(),
        &mut bp.hdr as *mut SetupHeader as *mut u8,
        hdr_copy_len,
    );

    // Override fields
    bp.hdr.type_of_loader = 0xFF; // undefined loader type
    bp.hdr.cmd_line_ptr = CMDLINE_ADDR as u32;

    // Write command line
    let cmdline_dest = guest_mem.add(CMDLINE_ADDR);
    std::ptr::copy_nonoverlapping(
        DEFAULT_CMDLINE.as_ptr(),
        cmdline_dest,
        DEFAULT_CMDLINE.len(),
    );

    // Set up E820 memory map
    // Entry 0: low memory (0 .. 0x9FC00) = usable RAM
    bp.e820_table[0] = E820Entry {
        addr: 0,
        size: 0x9FC00,
        entry_type: E820_RAM,
    };
    // Entry 1: EBDA + BIOS (0x9FC00 .. 0x100000) = reserved
    bp.e820_table[1] = E820Entry {
        addr: 0x9FC00,
        size: 0x100000 - 0x9FC00,
        entry_type: E820_RESERVED,
    };
    // Entry 2: high memory (1MB .. mem_size) = usable RAM
    bp.e820_table[2] = E820Entry {
        addr: 0x100000,
        size: (mem_size - 0x100000) as u64,
        entry_type: E820_RAM,
    };
    bp.e820_entries = 3;

    tracing::info!(
        kernel_size,
        setup_sects,
        entry_point = KERNEL_LOAD_ADDR,
        "bzImage loaded"
    );

    Ok(KERNEL_LOAD_ADDR as u64)
}

// ── Command line helper ──────────────────────────────────

/// Set a custom kernel command line in guest memory.
///
/// Writes the command line string (null-terminated) to `CMDLINE_ADDR` and
/// updates the `cmd_line_ptr` field in boot_params at `BOOT_PARAMS_ADDR`.
///
/// # Safety
/// `guest_mem` must point to a valid, writable memory region large enough
/// to hold boot_params and the command line.
pub unsafe fn set_cmdline(guest_mem: *mut u8, cmdline: &str) {
    // Write null-terminated command line at CMDLINE_ADDR
    let cmdline_dest = guest_mem.add(CMDLINE_ADDR);
    let cmdline_bytes = cmdline.as_bytes();
    std::ptr::copy_nonoverlapping(cmdline_bytes.as_ptr(), cmdline_dest, cmdline_bytes.len());
    // Null-terminate
    *cmdline_dest.add(cmdline_bytes.len()) = 0;

    // Update cmd_line_ptr in boot_params
    let boot_params_ptr = guest_mem.add(BOOT_PARAMS_ADDR) as *mut BootParams;
    (*boot_params_ptr).hdr.cmd_line_ptr = CMDLINE_ADDR as u32;
}

// ═══════════════════════════════════════════════════════════
// Tests
// ═══════════════════════════════════════════════════════════

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_e820_entry_size() {
        // E820Entry should be 20 bytes (8 + 8 + 4, packed)
        assert_eq!(std::mem::size_of::<E820Entry>(), 20);
    }

    #[test]
    fn test_setup_header_size() {
        // SetupHeader should be 44 bytes (1 + 15 + 1 + 1 + 10 + 4 + 16)
        assert_eq!(std::mem::size_of::<SetupHeader>(), 48);
    }

    #[test]
    fn test_boot_params_field_offsets() {
        // Verify that key fields are at expected positions
        let bp = std::mem::MaybeUninit::<BootParams>::uninit();
        let base = bp.as_ptr() as usize;

        unsafe {
            let e820_entries_offset = &(*bp.as_ptr()).e820_entries as *const u8 as usize - base;
            assert_eq!(
                e820_entries_offset, 0x1E8,
                "e820_entries should be at 0x1E8"
            );

            let hdr_offset = &(*bp.as_ptr()).hdr as *const SetupHeader as usize - base;
            assert_eq!(hdr_offset, 0x1F0, "hdr should be at 0x1F0");
        }
    }

    #[test]
    fn test_loader_error_display() {
        let err = LoaderError::InvalidFormat("missing HdrS magic at 0x202");
        assert!(format!("{err}").contains("HdrS"));

        let err = LoaderError::TooLarge {
            image_size: 100,
            mem_size: 50,
        };
        assert!(format!("{err}").contains("too large"));

        let err = LoaderError::Io(std::io::Error::from_raw_os_error(2));
        assert!(format!("{err}").contains("I/O error"));
    }

    #[test]
    fn test_load_bzimage_missing_file() {
        let mem_size = 64 * 1024 * 1024; // 64 MB
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
        assert_ne!(guest_mem, libc::MAP_FAILED);

        let result =
            unsafe { load_bzimage(guest_mem as *mut u8, mem_size, "/nonexistent/vmlinuz") };
        assert!(result.is_err());
        match result {
            Err(LoaderError::Io(_)) => {} // expected
            other => panic!("expected Io error, got: {other:?}"),
        }

        unsafe {
            libc::munmap(guest_mem, mem_size);
        }
    }

    #[test]
    fn test_load_bzimage_invalid_magic() {
        // Create a temp file with invalid content
        let dir = std::env::temp_dir();
        let path = dir.join("test_invalid_bzimage.bin");
        let mut data = vec![0u8; 0x1000];
        // Write wrong magic at 0x202
        data[0x202] = b'X';
        data[0x203] = b'X';
        data[0x204] = b'X';
        data[0x205] = b'X';
        std::fs::write(&path, &data).unwrap();

        let mem_size = 64 * 1024 * 1024;
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
        assert_ne!(guest_mem, libc::MAP_FAILED);

        let result =
            unsafe { load_bzimage(guest_mem as *mut u8, mem_size, path.to_str().unwrap()) };
        assert!(result.is_err());
        match result {
            Err(LoaderError::InvalidFormat(msg)) => {
                assert!(msg.contains("HdrS"), "error should mention HdrS: {msg}");
            }
            other => panic!("expected InvalidFormat, got: {other:?}"),
        }

        unsafe {
            libc::munmap(guest_mem, mem_size);
        }
        let _ = std::fs::remove_file(&path);
    }

    #[test]
    fn test_constants() {
        assert_eq!(BOOT_PARAMS_ADDR, 0x7000);
        assert_eq!(CMDLINE_ADDR, 0x20000);
        assert_eq!(KERNEL_LOAD_ADDR, 0x100000);
        assert_eq!(E820_RAM, 1);
        assert_eq!(E820_RESERVED, 2);
    }
}
