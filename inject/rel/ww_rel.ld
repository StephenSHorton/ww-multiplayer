/* ww_rel.ld: the game symbols the REL module (rel_main.c) references
 * by address. Each line makes an absolute (SHN_ABS) symbol in the
 * relocatable ELF; the ELF-to-REL converter (wwapi/rel) turns the
 * R_PPC_ADDR32 relocations against them into module-0 (DOL)
 * relocations the game's OSLink resolves at load time.
 *
 * Only DATA symbols belong here: every game FUNCTION the module calls
 * goes through a wwapi.h-style absolute function-pointer cast, which
 * compiles to lis/addi + mtctr + bctrl and needs no relocation at all.
 *
 * Addresses: zeldaret/tww config/GZLE01/symbols.txt (GZLE01, the NTSC-U
 * disc). Used with `powerpc-eabi-ld --relocatable -T ww_rel.ld`
 * (inject/build.py build_rel).
 */

/* f_pc/f_pc_leaf.h: leafdraw_method_class g_fpcLf_Method (0x14 B),
 * the leaf process methods every actor profile points at (its
 * process_profile_definition.sub_method, offset 0x0C). */
g_fpcLf_Method = 0x803726E8;

/* f_op/f_op_actor.h: actor_method_class g_fopAc_Method (0x20 B), the
 * actor-layer methods (leaf_process_profile_definition.sub_method,
 * offset 0x1C). */
g_fopAc_Method = 0x80371FF8;
