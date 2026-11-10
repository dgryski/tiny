package rc522

import (
	"errors"
	"time"
)

const fifoSize = 64

var (
	ErrNoTag       = errors.New("no tag in range")
	ErrAuthFailed  = errors.New("authentication failed")
	ErrWrite       = errors.New("write failed")
	ErrReadTimeout = errors.New("read failed: timeout")
	ErrRead        = errors.New("read failed")
	ErrNoDevice    = errors.New("no response from device")
	ErrCRC         = errors.New("crc mismatch")
	ErrNAK         = errors.New("card replied with NAK")
	ErrCollision   = errors.New("collision detected")
)

func (d *Device) Init() error {
	d.initPins()

	// Pulling reset low then high triggers a hard reset.
	d.reset.Low()
	time.Sleep(time.Millisecond)
	d.reset.High()
	// Oscillator start-up time, MFRC522 datasheet section 8.8.2.
	time.Sleep(50 * time.Millisecond)

	if err := d.writeRegister(regCommand, pcdSoftReset); err != nil {
		return err
	}
	// Wait for the PowerDown bit to clear.
	for i := 0; i < 3; i++ {
		time.Sleep(50 * time.Millisecond)
		cmd, err := d.readRegister(regCommand)
		if err != nil {
			return err
		}
		if cmd&(1<<4) == 0 {
			break
		}
	}

	version, err := d.GetVersion()
	if err != nil {
		return err
	}
	if version == 0x00 || version == 0xFF {
		return ErrNoDevice
	}

	for i := range initSequence {
		if err := d.writeRegister(initSequence[i].reg, initSequence[i].val); err != nil {
			return err
		}
	}

	return d.AntennaOn()
}

// TODO(dgryski): read many
func (d *Device) readRegister(reg byte) (byte, error) {
	address := ((reg << 1) & 0x7E) | 0x80
	d.cs.Low()
	defer d.cs.High()
	if _, err := d.bus.Transfer(address); err != nil {
		return 0, err
	}
	return d.bus.Transfer(0)
}

func (d *Device) writeRegister(reg, val byte) error {
	address := (reg << 1) & 0x7E
	d.cs.Low()
	defer d.cs.High()
	if _, err := d.bus.Transfer(address); err != nil {
		return err
	}
	_, err := d.bus.Transfer(val)
	return err
}

func (d *Device) writeRegisterData(reg byte, vals []byte) error {
	address := (reg << 1) & 0x7E
	d.cs.Low()
	defer d.cs.High()
	if _, err := d.bus.Transfer(address); err != nil {
		return err
	}
	for _, val := range vals {
		if _, err := d.bus.Transfer(val); err != nil {
			return err
		}
	}
	return nil
}

// TODO(dgryski): add readregister n -> []byte

func (d *Device) GetVersion() (byte, error) {
	return d.readRegister(regVersion)
}

func (d *Device) AntennaOn() error {
	curr, err := d.readRegister(regTxControl)
	if err != nil {
		return err
	}
	if (curr & 0x03) != 0x03 {
		return d.writeRegister(regTxControl, curr|0x03)
	}
	return nil
}

func (d *Device) AntennaOff() {
	d.clearFlags(regTxControl, 0x03)
}

func (d *Device) GetAntennaGain() (byte, error) {
	g, err := d.readRegister(regRFCfg)
	return g & (0x07 << 4), err
}

func (d *Device) SetAntennaGain(gain byte) error {
	g, err := d.GetAntennaGain()
	if err != nil {
		return err
	}

	if g != gain {
		// clear needed to allow 000 pattern;
		if err := d.clearFlags(regRFCfg, (0x07 << 4)); err != nil {
			return err
		}
		if err := d.setFlags(regRFCfg, gain&(0x07<<4)); err != nil {
			return err
		}
	}
	return nil
}

// Request reports whether a card answered REQA. No answer is not an error.
func (d *Device) Request() (bool, error) {
	return d.request_or_wakeup(piccREQA)
}

// Wakeup reports whether a card answered WUPA. No answer is not an error.
func (d *Device) Wakeup() (bool, error) {
	return d.request_or_wakeup(piccWUPA)
}

func (d *Device) request_or_wakeup(mode byte) (bool, error) {
	if err := d.writeRegister(regTxMode, 0x00); err != nil {
		return false, err
	}
	if err := d.writeRegister(regRxMode, 0x00); err != nil {
		return false, err
	}
	if err := d.writeRegister(regModWidth, 0x26); err != nil {
		return false, err
	}
	if err := d.clearFlags(regColl, 0x80); err != nil {
		return false, err
	}

	// REQA and WUPA are short frames of 7 bits.
	back, lastBits, err := d.communicateWithPICC(pcdTransceive, []byte{mode}, 7, 0)
	if errors.Is(err, ErrReadTimeout) {
		return false, nil
	}
	// Several cards answered, so at least one is present.
	if errors.Is(err, ErrCollision) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if len(back) != 2 || lastBits != 0 {
		return false, errors.New("wrong number of bits read")
	}
	return true, nil
}

// communicateWithPICC runs cmd and returns the received bytes and the number
// of valid bits in the last byte, where 0 means all 8 bits are valid. On
// ErrCollision the received data is returned as well.
func (d *Device) communicateWithPICC(cmd byte, data []byte, txLastBits byte, rxAlign byte) ([]byte, byte, error) {
	var irqWait byte

	switch cmd {
	case pcdMFAuthent:
		irqWait = 0x10
	case pcdTransceive:
		irqWait = 0x30
	}

	// TODO(dgryski): set command list slice?

	if err := d.writeRegister(regCommand, pcdIdle); err != nil {
		return nil, 0, err
	}

	if err := d.writeRegister(regComIrq, 0x7F); err != nil {
		return nil, 0, err
	}

	if err := d.writeRegister(regFIFOLevel, 0x80); err != nil {
		return nil, 0, err
	}

	if err := d.writeRegisterData(regFIFOData, data); err != nil {
		return nil, 0, err
	}

	if err := d.writeRegister(regBitFraming, rxAlign<<4|txLastBits); err != nil {
		return nil, 0, err
	}

	if err := d.writeRegister(regCommand, cmd); err != nil {
		return nil, 0, err
	}

	if cmd == pcdTransceive {
		if err := d.setFlags(regBitFraming, 0x80); err != nil {
			return nil, 0, err
		}
	}

	var n byte

	deadline := time.Now().Add(36 * time.Millisecond)
	var completed bool
	for time.Now().Before(deadline) {
		var err error
		if n, err = d.readRegister(regComIrq); err != nil {
			return nil, 0, err
		}
		if (n & irqWait) != 0 {
			completed = true
			break
		}
		if n&0x01 != 0 {
			return nil, 0, ErrReadTimeout
		}
		time.Sleep(1 * time.Microsecond)
	}

	if !completed {
		return nil, 0, ErrReadTimeout
	}

	errReg, err := d.readRegister(regError)
	if err != nil {
		return nil, 0, err
	}

	if errReg&0x13 != 0 {
		// BufferOvfl ParityErr ProtocolErr
		return nil, 0, ErrRead
	}

	var backData []byte
	var lastBits byte

	if cmd == pcdTransceive {
		n, err := d.readRegister(regFIFOLevel)
		if err != nil {
			return nil, 0, err
		}
		if n > fifoSize {
			return nil, 0, ErrRead
		}

		backData = make([]byte, n)
		for i := byte(0); i < n; i++ {
			byteVal, err := d.readRegister(regFIFOData)
			if err != nil {
				return nil, 0, err
			}
			backData[i] = byteVal
		}

		control, err := d.readRegister(regControl)
		if err != nil {
			return nil, 0, err
		}
		lastBits = control & 0x07
	}

	// The received data is still returned so the caller can resolve it.
	if errReg&0x08 != 0 {
		return backData, lastBits, ErrCollision
	}

	return backData, lastBits, nil
}

func (d *Device) setFlags(register byte, mask byte) error {
	current, err := d.readRegister(register)
	if err != nil {
		return err
	}
	return d.writeRegister(register, current|mask)
}

func (d *Device) clearFlags(register byte, mask byte) error {
	current, err := d.readRegister(register)
	if err != nil {
		return err
	}
	return d.writeRegister(register, current&^mask)
}

func (d *Device) ReadUID() (uid []byte, err error) {
	uuid, err := d.selectCard()
	return uuid, err
}

// selectCard selects the card and returns its UID.
func (d *Device) selectCard() ([]byte, error) {
	var uid []byte
	for _, cl := range []byte{piccSEL_CL1, piccSEL_CL2, piccSEL_CL3} {
		frame, err := d.anticollision(cl)
		if err != nil {
			return nil, err
		}

		sak, err := d.selectTag(frame, cl)
		if err != nil {
			return nil, err
		}

		// SAK bit 3 means the UID is not complete (ISO/IEC 14443-3).
		if sak&0x04 == 0 {
			return append(uid, frame[:4]...), nil
		}

		if frame[0] != piccCT {
			return nil, ErrRead
		}
		uid = append(uid, frame[1:4]...)
	}

	return nil, ErrRead
}

func (d *Device) anticollision(cascadeLevel byte) ([]byte, error) {
	if err := d.clearFlags(regColl, 0x80); err != nil {
		return nil, err
	}

	backData, _, err := d.communicateWithPICC(pcdTransceive, []byte{cascadeLevel, 0x20}, 0, 0)
	if err != nil {
		return nil, err
	}

	if len(backData) != 5 {
		return nil, errors.New("bad data length")
	}

	var bcc byte
	for _, v := range backData[:4] {
		bcc ^= v
	}

	if bcc != backData[4] {
		return nil, errors.New("bcc mismatch")
	}

	return backData, nil
}

// selectTag selects the card by UID and returns its SAK.
func (d *Device) selectTag(serial []byte, cl byte) (byte, error) {
	var bptr int
	buf := make([]byte, 7+2)
	buf[0] = cl
	buf[1] = 0x70
	bptr += 2
	copy(buf[bptr:], serial)
	bptr += len(serial)
	lsb, msb, err := d.calculateCRC(buf[:bptr])
	if err != nil {
		return 0, err
	}
	buf[bptr] = lsb
	bptr++
	buf[bptr] = msb
	bptr++

	backData, lastBits, err := d.communicateWithPICC(pcdTransceive, buf[:bptr], 0, 0)
	if err != nil {
		return 0, err
	}

	// The SAK is one byte followed by a CRC_A.
	if len(backData) != 3 || lastBits != 0 {
		return 0, ErrRead
	}
	lsb, msb, err = d.calculateCRC(backData[:1])
	if err != nil {
		return 0, err
	}
	if backData[1] != lsb || backData[2] != msb {
		return 0, ErrCRC
	}
	return backData[0], nil
}

// calculateCRC calculates the CRC of the data using the card chip.
func (d *Device) calculateCRC(data []byte) (byte, byte, error) {

	// TODO(dgryski): slice of commands

	if err := d.writeRegister(regCommand, pcdIdle); err != nil {
		return 0, 0, err
	}

	if err := d.writeRegister(regDivIrq, 0x04); err != nil {
		return 0, 0, err
	}
	if err := d.writeRegister(regFIFOLevel, 0x80); err != nil {
		return 0, 0, err
	}

	if err := d.writeRegisterData(regFIFOData, data); err != nil {
		return 0, 0, err
	}

	if err := d.writeRegister(regCommand, pcdCalcCRC); err != nil {
		return 0, 0, err
	}

	deadline := time.Now().Add(89 * time.Millisecond)

	var completed bool
	for time.Now().Before(deadline) {
		n, err := d.readRegister(regDivIrq)
		if err != nil {
			return 0, 0, err
		}
		if n&0x04 != 0 {
			completed = true
			break
		}
	}

	if !completed {
		return 0, 0, ErrReadTimeout
	}

	if err := d.writeRegister(regCommand, pcdIdle); err != nil {
		return 0, 0, err
	}

	lsb, err := d.readRegister(regCRCResultL)
	if err != nil {
		return 0, 0, err
	}

	msb, err := d.readRegister(regCRCResultH)
	if err != nil {
		return 0, 0, err
	}
	return lsb, msb, nil
}

func (d *Device) Authenticate(mode byte, block byte, key []byte, uid []byte) error {
	if len(key) != 6 || len(uid) < 4 {
		// TODO(dgryski): create a distinct error here?
		return ErrAuthFailed
	}
	buf := make([]byte, 12)
	buf[0] = mode
	buf[1] = block
	copy(buf[2:], key)
	// Use the last 4 UID bytes, NXP AN10927 section 3.2.5.
	copy(buf[8:], uid[len(uid)-4:])

	if _, _, err := d.communicateWithPICC(pcdMFAuthent, buf, 0, 0); err != nil {
		return ErrAuthFailed
	}

	status, err := d.readRegister(regStatus2)
	if err != nil {
		return ErrAuthFailed
	}

	if (status & 0x08) == 0 {
		return ErrAuthFailed
	}
	return nil
}

// TODO: sector/alignment stuff
func (d *Device) ReadBlock(block byte) ([]byte, error) {
	buf := make([]byte, 4)
	buf[0] = piccMF_READ
	buf[1] = block
	lsb, msb, err := d.calculateCRC(buf[:2])
	if err != nil {
		return nil, err
	}
	buf[2] = lsb
	buf[3] = msb

	backData, lastBits, err := d.communicateWithPICC(pcdTransceive, buf, 0, 0)
	if err != nil {
		return nil, err
	}

	// A MIFARE Classic NAK is a single 4 bit frame.
	if len(backData) == 1 && lastBits == 4 {
		return nil, ErrNAK
	}
	// 16 data bytes followed by a CRC_A.
	if len(backData) != 18 || lastBits != 0 {
		return nil, ErrRead
	}
	lsb, msb, err = d.calculateCRC(backData[:16])
	if err != nil {
		return nil, err
	}
	if backData[16] != lsb || backData[17] != msb {
		return nil, ErrCRC
	}

	return backData[:16], nil
}

// WriteBlock writes 16 bytes to a block of an authenticated sector.
func (d *Device) WriteBlock(block byte, data []byte) error {
	if len(data) != 16 {
		return ErrWrite
	}
	if err := d.transceiveACK([]byte{piccMF_WRITE, block}); err != nil {
		return err
	}
	return d.transceiveACK(data)
}

// transceiveACK sends buf with a CRC_A and expects a 4 bit ACK.
func (d *Device) transceiveACK(buf []byte) error {
	lsb, msb, err := d.calculateCRC(buf)
	if err != nil {
		return err
	}
	frame := append(append([]byte{}, buf...), lsb, msb)

	back, lastBits, err := d.communicateWithPICC(pcdTransceive, frame, 0, 0)
	if err != nil {
		return err
	}
	if len(back) != 1 || lastBits != 4 || back[0] != mfACK {
		return ErrWrite
	}
	return nil
}

func (d *Device) HaltA() error {
	buf := make([]byte, 4)
	buf[0] = piccHALT
	buf[1] = 0x00

	lsb, msb, err := d.calculateCRC(buf[:2])
	if err != nil {
		return err
	}
	buf[2] = lsb
	buf[3] = msb

	_, _, err = d.communicateWithPICC(pcdTransceive, buf, 0, 0)
	if errors.Is(err, ErrReadTimeout) {
		return nil
	}
	if err == nil {
		// A card that answers HLTA has not halted.
		return ErrRead
	}
	return err
}

func (d *Device) StopCrypto() {
	d.clearFlags(regStatus2, 0x08)
}

var initSequence = []struct{ reg, val byte }{
	{regTxMode, 0x00},
	{regRxMode, 0x00},
	{regModWidth, 0x26},
	{regTMode, 0x80},
	{regTPrescaler, 0xA9},
	{regTReloadH, 0x03},
	{regTReloadL, 0xE8},

	{regTxASK, 0x40},
	{regMode, 0x3D},
}
