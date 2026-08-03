package rc522

// From https://www.nxp.com/docs/en/data-sheet/MFRC522.pdf

// Register constants
const (
	// Page 0: Command and status
	regCommand    = 0x01 // starts and stops command execution
	regComIEn     = 0x02 // enable and disable interrupt request control bits
	regDivIEn     = 0x03 // enable and disable interrupt request control bits
	regComIrq     = 0x04 // interrupt request bits
	regDivIrq     = 0x05 // interrupt request bits
	regError      = 0x06 // error bits showing the error status of the last command executed
	regStatus1    = 0x07 // communication status bits
	regStatus2    = 0x08 // receiver and transmitter status bits
	regFIFOData   = 0x09 // input and output of 64 byte FIFO buffer
	regFIFOLevel  = 0x0A // number of bytes stored in the FIFO buffer
	regWaterLevel = 0x0B // level for FIFO underflow and overflow warning
	regControl    = 0x0C // miscellaneous control registers
	regBitFraming = 0x0D // adjustments for bit-oriented frames
	regColl       = 0x0E // bit position of the first bit-collision detected on the RF interface

	// Page 1: Command
	regMode        = 0x11 // defines general modes for transmitting and receiving
	regTxMode      = 0x12 // defines transmission data rate and framing
	regRxMode      = 0x13 // defines reception data rate and framing
	regTxControl   = 0x14 // controls the logical behavior of the antenna driver pins TX1 and TX2
	regTxASK       = 0x15 // controls the setting of the transmission modulation
	regTxSel       = 0x16 // selects the internal sources for the antenna driver
	regRxSel       = 0x17 // selects internal receiver settings
	regRxThreshold = 0x18 // selects thresholds for the bit decoder
	regDemod       = 0x19 // defines demodulator settings
	regMfTx        = 0x1C // controls some MIFARE communication transmit parameters
	regMfRx        = 0x1D // controls some MIFARE communication receive parameters
	regSerialSpeed = 0x1F // selects the speed of the serial UART interface

	// Page 2: Configuration

	regCRCResultH   = 0x21 // shows the MSB and LSB values of the CRC calculation
	regCRCResultL   = 0x22 //
	regModWidth     = 0x24 // controls the ModWidth setting
	regRFCfg        = 0x26 // configures the receiver gain
	regGsN          = 0x27 // selects the conductance of the antenna driver pins TX1 and TX2 for modulation
	regCWGsP        = 0x28 // defines the conductance of the p-driver output during periods of no modulation
	regModGsP       = 0x29 // defines the conductance of the p-driver output during periods of modulation
	regTMode        = 0x2A // defines settings for the internal timer
	regTPrescaler   = 0x2B // Table 107 on page 61
	regTReloadH     = 0x2C // defines the 16-bit timer reload value
	regTReloadL     = 0x2D // defines the 16-bit timer reload value
	regTCounterValH = 0x2E // shows the 16-bit timer value
	regTCounterValL = 0x2F // shows the 16-bit timer value

	// Page 3: Test register

	regTestSel1     = 0x31 // general test signal configuration
	regTestSel2     = 0x32 // general test signal configuration and PRBS control
	regTestPinEn    = 0x33 // enables pin output driver on pins D1 to D7
	regTestPinValue = 0x34 // defines the values for D1 to D7 when it is used as an I/O bus
	regTestBus      = 0x35 // shows the status of the internal test bus
	regAutoTest     = 0x36 // controls the digital self test
	regVersion      = 0x37 // shows the software version
	regAnalogTest   = 0x38 // controls the pins AUX1 and AUX2
	regTestDAC1     = 0x39 // defines the test value for TestDAC1
	regTestDAC2     = 0x3A // defines the test value for TestDAC2
	regTestADC      = 0x3B // shows the value of ADC I and Q channels
)
