package addr

// What wwapi/crash reads after the game stops on an exception: the record
// JUTException's error handler fills, the OSContext it points at, and the
// OS's list of linked REL modules. Read only; nothing here is written.

// exCallbackObject, JUTException.cpp's static CallbackObject (struct at
// JUTException.cpp:21-27). errorHandler fills it just before it hands the
// fault to the exception thread (JUTException.cpp:209-213); context is 0
// until the first exception.
const (
	ExCallbackObject     uint32 = 0x803ED988 // sym:18042
	ExCallbackObjectSize        = 0x14       // sym:18042

	ExCallbackOffError   = 0x04 // u16 OSError (OSError.h:15-31)
	ExCallbackOffContext = 0x08 // OSContext*
	// param_3 and param_4 are the handler's dsisr and dar
	// (OSError.c:154, :174): the faulting access for a DSI.
	ExCallbackOffDSISR = 0x0C
	ExCallbackOffDAR   = 0x10
)

// OSContext members (OSContext.h:138-153), offsets from the context.
const (
	OSContextOffGPR  = 0x000 // u32[32]
	OSContextOffCR   = 0x080
	OSContextOffLR   = 0x084
	OSContextOffCTR  = 0x088
	OSContextOffXER  = 0x08C
	OSContextOffSRR0 = 0x198 // the faulting PC
	OSContextOffSRR1 = 0x19C // MSR at the fault
	// OSContextIntSize covers the integer state through srr1, all a
	// crash report reads (the FPRs, mode, state and GQRs come after).
	OSContextIntSize = 0x1A0
)

// __OSModuleList, the OSModuleQueue {head, tail} of linked REL modules
// (OSLink.h:27), and the OSModuleInfo it chains (OSLink.h:43-51).
const (
	OSModuleList uint32 = 0x800030C8 // OSLink.h:27 (AT_ADDRESS)

	OSModuleInfoOffID          = 0x00
	OSModuleInfoOffNext        = 0x04 // link.next
	OSModuleInfoOffNumSections = 0x0C
	OSModuleInfoOffSectionInfo = 0x10 // an absolute pointer once linked
	// OSSectionInfoSize is one {offset, size} entry (OSLink.h:38-41).
	// The low bit of offset is OS_SECTIONINFO_EXEC (OSLink.h:81-82).
	OSSectionInfoSize = 8
	OSSectionInfoExec = 0x1
)
