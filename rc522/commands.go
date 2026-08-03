package rc522

const (

	// PCD commands
	pcdIdle             = 0b00000000 // no action, cancels current command execution
	pcdMem              = 0b00000001 // stores 25 bytes into the internal buffer
	pcdGenerateRandomID = 0b00000010 // generates a 10-byte random ID number
	pcdCalcCRC          = 0b00000011 // activates the CRC coprocessor or performs a self test
	pcdTransmit         = 0b00000100 // transmits data from the FIFO buffer
	pcdNoChange         = 0b00000111 // no command change
	pcdReceive          = 0b00001000 // activates the receiver circuits
	pcdTransceive       = 0b00001100 // transmits data from FIFO buffer to antenna and automatically activates the receiver after transmission
	pcdMFAuthent        = 0b00001110 // performs the MIFARE standard authentication as a reader
	pcdSoftReset        = 0b00001111 // resets the MFRC522

	// PICC commands

	// https://www.nxp.com/docs/en/data-sheet/MF1S50YYX_V1.pdf
	piccREQA    = 0x26 // REQuest command, Type A. Invites PICCs in state IDLE to go to READY and prepare for anticollision or selection. 7 bit frame.
	piccWUPA    = 0x52 // Wake-UP command, Type A. Invites PICCs in state IDLE and HALT to go to READY(*) and prepare for anticollision or selection. 7 bit frame.
	piccCT      = 0x88 // Cascade Tag. Not really a command, but used during anti collision.
	piccSEL_CL1 = 0x93 // Anti collision/Select, Cascade Level 1
	piccSEL_CL2 = 0x95 // Anti collision/Select, Cascade Level 2
	piccSEL_CL3 = 0x97 // Anti collision/Select, Cascade Level 3
	piccHALT    = 0x50 // HaLT command, Type A. Instructs an ACTIVE PICC to go to state HALT.
	piccRATS    = 0xE0 // Request command for Answer To Reset.

	piccMF_AUTH_KEY_A = 0x60 // Perform authentication with Key A
	piccMF_AUTH_KEY_B = 0x61 // Perform authentication with Key B
	piccMF_READ       = 0x30 // Reads one 16 byte block from the authenticated sector of the PICC. Also used for MIFARE Ultralight.
	piccMF_WRITE      = 0xA0 // Writes one 16 byte block to the authenticated sector of the PICC. Called "COMPATIBILITY WRITE" for MIFARE Ultralight.
	piccMF_DECREMENT  = 0xC0 // Decrements the contents of a block and stores the result in the internal data register.
	piccMF_INCREMENT  = 0xC1 // Increments the contents of a block and stores the result in the internal data register.
	piccMF_RESTORE    = 0xC2 // Reads the contents of a block into the internal data register.
	piccMF_TRANSFER   = 0xB0 // Writes the contents of the internal data register to a block.

	// The commands used for MIFARE Ultralight (from http://www.nxp.com/documents/data_sheet/MF0ICU1.pdf Section 8.6)
	// The piccMF_READ and PICC_CMD_MF_WRITE can also be used for MIFARE Ultralight.
	piccUL_WRITE = 0xA2 // Writes one 4 byte page to the PICC.
)

const (
	AuthKeyA = piccMF_AUTH_KEY_A
	AuthKeyB = piccMF_AUTH_KEY_B
)
