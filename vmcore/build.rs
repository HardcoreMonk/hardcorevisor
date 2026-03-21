fn main() {
    let crate_dir = std::env::var("CARGO_MANIFEST_DIR").unwrap();

    // Generate vmcore.h C header via cbindgen
    match cbindgen::Builder::new()
        .with_crate(&crate_dir)
        .with_config(cbindgen::Config::from_file("cbindgen.toml").unwrap_or_default())
        .generate()
    {
        Ok(bindings) => {
            bindings.write_to_file("vmcore.h");
            println!("cargo:warning=vmcore.h generated successfully");
        }
        Err(e) => {
            eprintln!("cbindgen failed: {e}");
            // Don't fail the build — header generation is optional during dev
        }
    }

    // Re-run if any source file changes
    println!("cargo:rerun-if-changed=src/");
    println!("cargo:rerun-if-changed=cbindgen.toml");
}
