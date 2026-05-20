use h3o::LatLng;
use std::slice;

#[no_mangle]
pub extern "C" fn compute_resolutions_batch(
    lats: *const f64,
    lngs: *const f64,
    count: libc::size_t,
    out_res2: *mut u64,
    out_res4: *mut u64,
    out_res6: *mut u64,
    out_res7: *mut u64,
) {
    if lats.is_null() || lngs.is_null() || out_res2.is_null() || out_res4.is_null() || out_res6.is_null() || out_res7.is_null() {
        return;
    }

    unsafe {
        let lats_slice = slice::from_raw_parts(lats, count);
        let lngs_slice = slice::from_raw_parts(lngs, count);
        let res2_slice = slice::from_raw_parts_mut(out_res2, count);
        let res4_slice = slice::from_raw_parts_mut(out_res4, count);
        let res6_slice = slice::from_raw_parts_mut(out_res6, count);
        let res7_slice = slice::from_raw_parts_mut(out_res7, count);

        #[allow(clippy::needless_range_loop)]
        for i in 0..count {
            if let Ok(ll) = LatLng::new(lats_slice[i], lngs_slice[i]) {
                res2_slice[i] = ll.to_cell(h3o::Resolution::Two).into();
                res4_slice[i] = ll.to_cell(h3o::Resolution::Four).into();
                res6_slice[i] = ll.to_cell(h3o::Resolution::Six).into();
                res7_slice[i] = ll.to_cell(h3o::Resolution::Seven).into();
            } else {
                res2_slice[i] = 0;
                res4_slice[i] = 0;
                res6_slice[i] = 0;
                res7_slice[i] = 0;
            }
        }
    }
}
